package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// Backup creates a consistent standalone database snapshot through SQLite's
// online backup API. Artifact files must be snapshotted by the caller as part
// of the same higher-level restore unit.
func (store *Store) Backup(ctx context.Context, destination string) (resultErr error) {
	if err := validatePath(destination); err != nil {
		return err
	}
	if destination == store.path {
		return fmt.Errorf("backup destination must differ from the source database")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create backup parent directory: %w", err)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("%w: %s", ErrExists, destination)
		}
		return fmt.Errorf("create backup database: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	completed := false
	defer func() {
		if resultErr != nil && !completed {
			_ = os.Remove(destination)
		}
	}()
	destinationDB, err := connect(ctx, destination, "rw")
	if err != nil {
		return err
	}
	defer destinationDB.Close()
	if err := runBackup(ctx, destinationDB, store.db); err != nil {
		return err
	}
	if err := destinationDB.Close(); err != nil {
		return fmt.Errorf("close backup database: %w", err)
	}
	if err := requireMode(destination); err != nil {
		return err
	}
	verification, err := Open(ctx, destination)
	if err != nil {
		return fmt.Errorf("verify backup database: %w", err)
	}
	if err := verification.Close(); err != nil {
		return fmt.Errorf("close verified backup: %w", err)
	}
	completed = true
	return nil
}

func runBackup(ctx context.Context, destination, source *sql.DB) error {
	sourceConn, err := source.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire source backup connection: %w", err)
	}
	defer sourceConn.Close()
	destinationConn, err := destination.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire destination backup connection: %w", err)
	}
	defer destinationConn.Close()
	return destinationConn.Raw(func(destinationDriver any) error {
		destinationSQLite, ok := destinationDriver.(*sqlite3.SQLiteConn)
		if !ok {
			return fmt.Errorf("destination is not a go-sqlite3 connection")
		}
		return sourceConn.Raw(func(sourceDriver any) error {
			sourceSQLite, ok := sourceDriver.(*sqlite3.SQLiteConn)
			if !ok {
				return fmt.Errorf("source is not a go-sqlite3 connection")
			}
			backup, err := destinationSQLite.Backup("main", sourceSQLite, "main")
			if err != nil {
				return classifySQLite("start SQLite backup", err)
			}
			for {
				if err := ctx.Err(); err != nil {
					_ = backup.Finish()
					return err
				}
				done, stepErr := backup.Step(128)
				if stepErr != nil {
					_ = backup.Finish()
					return classifySQLite("copy SQLite backup pages", stepErr)
				}
				if done {
					if err := backup.Finish(); err != nil {
						return classifySQLite("finish SQLite backup", err)
					}
					return nil
				}
			}
		})
	})
}
