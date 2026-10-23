// Package runtimedb resolves explicit database configuration and owns the pool
// and, only when requested, a private local PostgreSQL process.
package runtimedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/db"
	"github.com/Sahaj-Tech-ltd/overkill/internal/localdb"
)

type Options struct {
	URL      string
	Managed  bool
	BinDir   string
	DataDir  string
	HomeDir  string
	Required bool
}

// Owner.Context is cancelled if the owned database dies. Application resources
// must drain before Close, which closes the pool before stopping PostgreSQL.
type Owner struct {
	DB           *sql.DB
	Context      context.Context
	cancel       context.CancelFunc
	stopDatabase context.CancelFunc
	manager      *localdb.Manager
	failure      chan error
}

func Open(parent context.Context, options Options) (_ *Owner, resultErr error) {
	ctx, cancel := context.WithCancel(parent)
	owner := &Owner{Context: ctx, cancel: cancel, failure: make(chan error, 1)}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, owner.Close())
		}
	}()
	connection := options.URL
	if connection == "" && options.Managed {
		if options.BinDir == "" {
			return nil, fmt.Errorf("managed local database requires local_database.bin_dir or --local-database-bin pointing to installed PostgreSQL binaries; no binaries are downloaded")
		}
		dataDir := options.DataDir
		if dataDir == "" {
			dataDir = filepath.Join(options.HomeDir, "postgres")
		}
		manager, err := localdb.NewManager(localdb.Config{BinDir: options.BinDir, DataDir: dataDir})
		if err != nil {
			return nil, fmt.Errorf("managed local database setup: %w", err)
		}
		owner.manager = manager
		databaseCtx, stopDatabase := context.WithCancel(context.Background())
		owner.stopDatabase = stopDatabase
		cancelStartup := context.AfterFunc(ctx, stopDatabase)
		info, err := manager.Start(databaseCtx)
		cancelStartup()
		if err != nil {
			return nil, fmt.Errorf("managed local database startup: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		connection = info.DSN()
		go func() {
			if err := manager.Wait(ctx); err != nil && ctx.Err() == nil {
				owner.failure <- err
				cancel()
			}
		}()
	}
	if connection == "" {
		if options.Required {
			return nil, fmt.Errorf("PostgreSQL required: set DATABASE_URL, database_url, or explicitly enable local_database.enabled / --local-database")
		}
		return owner, nil
	}
	startup, done := context.WithTimeout(ctx, 10*time.Second)
	defer done()
	database, err := db.OpenContext(startup, connection)
	if err != nil {
		return nil, fmt.Errorf("database connection: %w", err)
	}
	owner.DB = database
	if err := db.Migrate(database); err != nil {
		return nil, fmt.Errorf("database migration: %w", err)
	}
	return owner, nil
}

func (owner *Owner) Close() error {
	owner.cancel()
	var result error
	if owner.DB != nil {
		result = owner.DB.Close()
	}
	if owner.manager != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		result = errors.Join(result, owner.manager.Stop(shutdown))
		cancel()
	}
	if owner.stopDatabase != nil {
		owner.stopDatabase()
	}
	select {
	case err := <-owner.failure:
		result = errors.Join(result, err)
	default:
	}
	return result
}
