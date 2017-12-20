package main

import (
	"context"
	"errors"
	"fmt"
	driver "github.com/arangodb/go-driver"
	"github.com/arangodb/go-driver/http"
)

const (
	mig_col string = "arangomigo"
)

type Migration interface {
	migrate(ctx context.Context, driver *driver.Database) error
	FileName() string
	SetFileName(name string)
	CheckSum() string
	SetCheckSum(sum string)
}

// Common operation implementations
func (op *Operation) FileName() string {
	return op.fileName
}

func (op *Operation) SetFileName(fileName string) {
	op.fileName = fileName
}

func (op *Operation) CheckSum() string {
	return op.checksum
}

func (op *Operation) SetCheckSum(sum string) {
	op.checksum = sum
}

// End Common operation implementations

// Entry point in actually executing the migrations
func perform(ctx context.Context, c Config) error {
	pm, err := migrations(c.MigrationsPath)
	if e(err) {
		return err
	}

	cl, err := client(c, ctx)
	db, err := loadDb(ctx, c, cl, &pm)

	err = migrateNow(ctx, db, pm)
	return err
}

// Processed marker. Declared here since it's impl related.
type migration struct {
	_key     string
	checksum string
}

func migrateNow(ctx context.Context, db driver.Database, pms []PairedMigrations) error {
	fmt.Println("Starting migration now")

	mcol, err := db.Collection(ctx, mig_col)
	if e(err) {
		return err
	}

	for _, pm := range pms {
		m := pm.change
		u := pm.undo

		// Since migrations are stored by their file names, just see if it exists
		migRan, err := mcol.DocumentExists(ctx, m.FileName())
		if e(err) {
			return err
		}

		if !migRan {
			err := m.migrate(ctx, &db)
			if !e(err) {
				if temp, ok := m.(*Database); !ok || temp.Action == MODIFY {
					_, err := mcol.CreateDocument(ctx, migration{_key: m.FileName(), checksum: m.CheckSum()})
					if e(err) {
						return err
					}
				}
			} else if e(err) && driver.IsArangoError(err) && u != nil {
				// This probably means a migration issue, back out.
				err = u.migrate(ctx, &db)
				if e(err) {
					return err
				}
			} else {
				return err
			}
		}
	}
	return nil
}

func loadDb(
	ctx context.Context,
	conf Config,
	cl driver.Client,
	pm *[]PairedMigrations) (driver.Database, error) {
	// Checks to see if the database exists
	dbName := conf.Db
	db, err := cl.Database(ctx, dbName)
	if err != nil && driver.IsNotFound(err) {
		// Creating a database requires extra setup.
		m := (*pm)[0].change
		o, ok := m.(*Database)
		if !ok {
			return nil, errors.New(fmt.Sprintf("Database %s does not exist and first migration is not the DB creation", dbName))
		}
		if o.Name != dbName {
			return nil, errors.New("Configuration's dbname does not match migration name")
		}
		o.cl = cl
		err = m.migrate(ctx, &db)
		if err == nil {
			db = o.db
			fmt.Printf("Target db is now %s\n", db.Name())
		}
	} else if err == nil {
		m := (*pm)[0].change
		switch m.(type) {
		case *Database:
			*pm = (*pm)[1:]
		}
	}

	if err == nil {
		// Check to see if the migration coll is there.
		_, err := db.Collection(ctx, mig_col)
		if driver.IsNotFound(err) {
			db.CreateCollection(ctx, mig_col, nil)
		}
	}

	return db, err
}

// Create the client used to talk to ArangoDB
func client(c Config, ctx context.Context) (driver.Client, error) {
	conn, err := http.NewConnection(http.ConnectionConfig{
		Endpoints: c.Endpoints,
	})

	if e(err) {
		return nil, errors.New("Couldn't create connection to Arango\n" + err.Error())
	}
	cl, err := driver.NewClient(driver.ClientConfig{
		Connection:     conn,
		Authentication: driver.BasicAuthentication(c.Username, c.Password),
	})

	return cl, err
}

func e(err error) bool {
	return err != nil
}

func (d *Database) migrate(ctx context.Context, db *driver.Database) error {
	var oerr error = nil
	switch d.Action {
	case CREATE:
		if d.db != nil {
			return nil
		}
		options := driver.CreateDatabaseOptions{}
		active := true
		for _, u := range d.Allowed {
			options.Users = append(options.Users,
				driver.CreateDatabaseUserOptions{
					UserName: u.Username,
					Password: u.Password,
					Active:   &active,
				})
		}
		newdb, err := d.cl.CreateDatabase(ctx, d.Name, &options)
		if err == nil {
			d.db = newdb
		} else {
			oerr = err
		}
	case DELETE:
		err := (*db).Remove(ctx)
		if e(err) {
			oerr = err
		}
	default:
		oerr = errors.New(fmt.Sprintf("Database migration does not support op %s", d.Action))
	}

	return oerr
}

func (cl Collection) migrate(ctx context.Context, db *driver.Database) error {
	switch cl.Action {
	case DELETE:
	}
	return nil
}
