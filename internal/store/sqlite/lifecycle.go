// Package sqlite implements the schema 4 SQLite StateStore adapter.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"

	"github.com/yjrszcq/openvpn-docker/internal/buildinfo"
)

const (
	DefaultPath     = "/etc/openvpn/meta/state.db"
	DataSchema      = buildinfo.DataSchema
	InitialRevision = 1
	CurrentRevision = 6
	BusyTimeoutMS   = 30000
)

var (
	ErrMissing             = errors.New("SQLite database is missing")
	ErrExists              = errors.New("SQLite database already exists")
	ErrPermission          = errors.New("SQLite database permissions are unsafe")
	ErrCorrupt             = errors.New("SQLite database is corrupt")
	ErrSchema              = errors.New("SQLite database schema is invalid")
	ErrUnsupportedSchema   = errors.New("SQLite data schema is unsupported")
	ErrUnsupportedRevision = errors.New("SQLite migration revision is unsupported")
	ErrBusy                = errors.New("SQLite database is busy")
	ErrConstraint          = errors.New("SQLite constraint failed")
)

// Metadata is the schema identity stored inside one database.
type Metadata struct {
	DataSchema       int
	DatabaseRevision int
	CreatedVersion   string
	CreatedAt        time.Time
}

// Store owns the adapter's single SQLite connection.
type Store struct {
	db        *sql.DB
	path      string
	metadata  Metadata
	closeOnce sync.Once
	closeErr  error
}

// Create initializes a new schema 4 database without overwriting existing data.
func Create(ctx context.Context, path, createdVersion string) (_ *Store, resultErr error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	if createdVersion == "" || strings.ContainsAny(createdVersion, "\r\n") {
		return nil, fmt.Errorf("created version is invalid")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create SQLite parent directory: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrExists, path)
		}
		return nil, fmt.Errorf("create SQLite database %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close new SQLite database %s: %w", path, err)
	}
	initialized := false
	defer func() {
		if resultErr != nil && !initialized {
			_ = os.Remove(path)
		}
	}()
	database, err := connect(ctx, path, "rwc")
	if err != nil {
		return nil, err
	}
	defer func() {
		if resultErr != nil {
			_ = database.Close()
		}
	}()
	metadata, err := initialize(ctx, database, createdVersion, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	metadata, err = migrate(ctx, database, metadata)
	if err != nil {
		return nil, err
	}
	if err := integrityCheck(ctx, database); err != nil {
		return nil, err
	}
	if err := validateSchemaObjects(ctx, database); err != nil {
		return nil, err
	}
	if err := requireMode(path); err != nil {
		return nil, err
	}
	initialized = true
	return &Store{db: database, path: path, metadata: metadata}, nil
}

// Open validates and opens an existing schema 4 database without creating it.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	if err := requireMode(path); err != nil {
		return nil, err
	}
	database, err := connect(ctx, path, "rw")
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = database.Close()
		}
	}()
	if err := integrityCheck(ctx, database); err != nil {
		return nil, err
	}
	metadata, err := readMetadata(ctx, database)
	if err != nil {
		return nil, err
	}
	if err := validateMetadata(metadata); err != nil {
		return nil, err
	}
	openedRevision := metadata.DatabaseRevision
	metadata, err = migrate(ctx, database, metadata)
	if err != nil {
		return nil, err
	}
	if metadata.DatabaseRevision != openedRevision {
		if err := integrityCheck(ctx, database); err != nil {
			return nil, err
		}
	}
	if err := validateSchemaObjects(ctx, database); err != nil {
		return nil, err
	}
	closeOnError = false
	return &Store{db: database, path: path, metadata: metadata}, nil
}

// Close releases the adapter connection.
func (store *Store) Close() error {
	if store == nil || store.db == nil {
		return nil
	}
	store.closeOnce.Do(func() {
		store.closeErr = store.db.Close()
	})
	return store.closeErr
}

// Path returns the canonical database path used by this store.
func (store *Store) Path() string { return store.path }

// Metadata returns immutable schema identity read at open time.
func (store *Store) Metadata() Metadata { return store.metadata }

// IntegrityCheck performs a full SQLite integrity check.
func (store *Store) IntegrityCheck(ctx context.Context) error {
	return integrityCheck(ctx, store.db)
}

func validatePath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || strings.ContainsRune(path, '\x00') {
		return fmt.Errorf("SQLite database path must be a clean absolute path")
	}
	return nil
}

func requireMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrMissing, path)
		}
		return fmt.Errorf("stat SQLite database %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrPermission, path)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("%w: %s has mode %04o, want 0600", ErrPermission, path, info.Mode().Perm())
	}
	return nil
}

func connect(ctx context.Context, path, mode string) (*sql.DB, error) {
	query := url.Values{
		"mode":          []string{mode},
		"_busy_timeout": []string{fmt.Sprint(BusyTimeoutMS)},
		"_foreign_keys": []string{"on"},
		"_journal_mode": []string{"DELETE"},
		"_synchronous":  []string{"FULL"},
		"_txlock":       []string{"immediate"},
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, classifySQLite("open SQLite database", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, classifySQLite("open SQLite database", err)
	}
	return database, nil
}

func initialize(ctx context.Context, database *sql.DB, createdVersion string, createdAt time.Time) (Metadata, error) {
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return Metadata{}, classifySQLite("begin SQLite initialization", err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(ctx, `
CREATE TABLE schema_metadata (
    singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
    data_schema INTEGER NOT NULL CHECK (data_schema > 0),
    database_revision INTEGER NOT NULL CHECK (database_revision > 0),
    created_version TEXT NOT NULL CHECK (length(created_version) > 0),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0)
) STRICT`); err != nil {
		return Metadata{}, classifySQLite("create schema metadata", err)
	}
	metadata := Metadata{
		DataSchema:       DataSchema,
		DatabaseRevision: InitialRevision,
		CreatedVersion:   createdVersion,
		CreatedAt:        createdAt.Truncate(time.Second),
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO schema_metadata(singleton, data_schema, database_revision, created_version, created_at)
VALUES(1, ?, ?, ?, ?)`, metadata.DataSchema, metadata.DatabaseRevision, metadata.CreatedVersion, metadata.CreatedAt.Format(time.RFC3339)); err != nil {
		return Metadata{}, classifySQLite("write schema metadata", err)
	}
	if err := transaction.Commit(); err != nil {
		return Metadata{}, classifySQLite("commit SQLite initialization", err)
	}
	if err := integrityCheck(ctx, database); err != nil {
		return Metadata{}, err
	}
	return metadata, nil
}

func readMetadata(ctx context.Context, database *sql.DB) (Metadata, error) {
	var metadata Metadata
	var createdAt string
	err := database.QueryRowContext(ctx, `
SELECT data_schema, database_revision, created_version, created_at
FROM schema_metadata WHERE singleton = 1`).Scan(
		&metadata.DataSchema,
		&metadata.DatabaseRevision,
		&metadata.CreatedVersion,
		&createdAt,
	)
	if err != nil {
		return Metadata{}, fmt.Errorf("%w: read schema metadata: %v", ErrSchema, err)
	}
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Metadata{}, fmt.Errorf("%w: invalid schema creation time", ErrSchema)
	}
	metadata.CreatedAt = parsed
	return metadata, nil
}

func validateMetadata(metadata Metadata) error {
	if metadata.DataSchema != DataSchema {
		return fmt.Errorf("%w: database has data schema %d, require %d", ErrUnsupportedSchema, metadata.DataSchema, DataSchema)
	}
	if metadata.DatabaseRevision <= 0 {
		return fmt.Errorf("%w: invalid database revision %d", ErrSchema, metadata.DatabaseRevision)
	}
	if metadata.DatabaseRevision > CurrentRevision {
		return fmt.Errorf("%w: database has revision %d, latest supported is %d", ErrUnsupportedRevision, metadata.DatabaseRevision, CurrentRevision)
	}
	if metadata.CreatedVersion == "" || metadata.CreatedAt.IsZero() {
		return fmt.Errorf("%w: incomplete schema metadata", ErrSchema)
	}
	return nil
}

func integrityCheck(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return classifySQLite("run SQLite integrity check", err)
	}
	defer rows.Close()
	issues := make([]string, 0)
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return classifySQLite("read SQLite integrity check", err)
		}
		if result != "ok" {
			issues = append(issues, result)
		}
	}
	if err := rows.Err(); err != nil {
		return classifySQLite("read SQLite integrity check", err)
	}
	if len(issues) > 0 {
		return fmt.Errorf("%w: %s", ErrCorrupt, strings.Join(issues, "; "))
	}
	return foreignKeyCheck(ctx, database)
}

func foreignKeyCheck(ctx context.Context, database *sql.DB) error {
	rows, err := database.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return classifySQLite("run SQLite foreign key check", err)
	}
	defer rows.Close()
	issues := make([]string, 0)
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var foreignKey int
		if err := rows.Scan(&table, &rowID, &parent, &foreignKey); err != nil {
			return classifySQLite("read SQLite foreign key check", err)
		}
		row := "without-rowid"
		if rowID.Valid {
			row = fmt.Sprint(rowID.Int64)
		}
		issues = append(issues, fmt.Sprintf("%s row %s references %s (foreign key %d)", table, row, parent, foreignKey))
	}
	if err := rows.Err(); err != nil {
		return classifySQLite("read SQLite foreign key check", err)
	}
	if len(issues) > 0 {
		return fmt.Errorf("%w: %s", ErrCorrupt, strings.Join(issues, "; "))
	}
	return nil
}

func validateSchemaObjects(ctx context.Context, database *sql.DB) error {
	requiredTables := map[string]struct{}{
		"schema_metadata":     {},
		"instances":           {},
		"applied_config":      {},
		"networks":            {},
		"address_pools":       {},
		"pushed_routes":       {},
		"dns_servers":         {},
		"clients":             {},
		"address_assignments": {},
		"client_leases":       {},
		"artifacts":           {},
		"audit_events":        {},
		"operations":          {},
	}
	rows, err := database.QueryContext(ctx, `
SELECT name, sql FROM sqlite_schema
WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return classifySQLite("read SQLite schema tables", err)
	}
	found := make(map[string]struct{}, len(requiredTables))
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			_ = rows.Close()
			return classifySQLite("read SQLite schema table", err)
		}
		if _, expected := requiredTables[name]; !expected {
			_ = rows.Close()
			return fmt.Errorf("%w: unexpected table %s", ErrSchema, name)
		}
		if !strings.HasSuffix(strings.TrimSpace(definition), "STRICT") {
			_ = rows.Close()
			return fmt.Errorf("%w: table %s is not STRICT", ErrSchema, name)
		}
		found[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return classifySQLite("close SQLite schema tables", err)
	}
	if err := rows.Err(); err != nil {
		return classifySQLite("read SQLite schema tables", err)
	}
	for name := range requiredTables {
		if _, exists := found[name]; !exists {
			return fmt.Errorf("%w: required table %s is missing", ErrSchema, name)
		}
	}
	for _, name := range []string{
		"clients_current_name",
		"assignments_current_client_network",
		"assignments_current_network_address",
		"client_leases_network_address",
	} {
		var definition string
		if err := database.QueryRowContext(ctx, "SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = ?", name).Scan(&definition); err != nil {
			return fmt.Errorf("%w: required unique index %s is missing", ErrSchema, name)
		}
		if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(definition)), "CREATE UNIQUE INDEX") {
			return fmt.Errorf("%w: index %s is not unique", ErrSchema, name)
		}
	}
	return nil
}

func classifySQLite(operation string, err error) error {
	var sqliteError sqlite3.Error
	if errors.As(err, &sqliteError) {
		switch sqliteError.Code {
		case sqlite3.ErrCorrupt, sqlite3.ErrNotADB:
			return fmt.Errorf("%w: %s: %v", ErrCorrupt, operation, err)
		case sqlite3.ErrPerm, sqlite3.ErrReadonly, sqlite3.ErrCantOpen:
			return fmt.Errorf("%w: %s: %v", ErrPermission, operation, err)
		case sqlite3.ErrBusy, sqlite3.ErrLocked:
			return fmt.Errorf("%w: %s: %v", ErrBusy, operation, err)
		case sqlite3.ErrConstraint:
			return fmt.Errorf("%w: %s: %v", ErrConstraint, operation, err)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
