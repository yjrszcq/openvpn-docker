package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlite3 "github.com/mattn/go-sqlite3"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func databasePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "meta", "state.db")
}

func TestRuntimeOpenRequiresCurrentRevision(t *testing.T) {
	path := databasePath(t)
	store := createStore(t, path)
	if _, err := store.db.Exec("UPDATE schema_metadata SET database_revision = ?", CurrentRevision-1); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRuntime(context.Background(), path); !errors.Is(err, ErrUnsupportedRevision) {
		t.Fatalf("runtime old revision error=%v", err)
	}
}

func createStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Create(context.Background(), path, "4.0.0-test")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return store
}

func TestCreateAppliesLifecyclePolicy(t *testing.T) {
	path := databasePath(t)
	store := createStore(t, path)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode=%04o, want 0600", info.Mode().Perm())
	}
	metadata := store.Metadata()
	if metadata.DataSchema != DataSchema || metadata.DatabaseRevision != CurrentRevision || metadata.CreatedVersion != "4.0.0-test" || metadata.CreatedAt.IsZero() {
		t.Fatalf("unexpected metadata: %+v", metadata)
	}
	if store.Path() != path || store.db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("unexpected store policy: path=%q stats=%+v", store.Path(), store.db.Stats())
	}
	pragmas := []struct {
		query string
		want  string
	}{
		{"PRAGMA foreign_keys", "1"},
		{"PRAGMA journal_mode", "delete"},
		{"PRAGMA synchronous", "2"},
		{"PRAGMA busy_timeout", "30000"},
	}
	for _, pragma := range pragmas {
		var got string
		if err := store.db.QueryRow(pragma.query).Scan(&got); err != nil {
			t.Fatalf("%s: %v", pragma.query, err)
		}
		if got != pragma.want {
			t.Errorf("%s=%q, want %q", pragma.query, got, pragma.want)
		}
	}
	var definition string
	if err := store.db.QueryRow("SELECT sql FROM sqlite_schema WHERE name = 'schema_metadata'").Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(definition, "STRICT") {
		t.Fatalf("schema_metadata is not strict: %s", definition)
	}
	if err := store.IntegrityCheck(context.Background()); err != nil {
		t.Fatalf("integrity check: %v", err)
	}
}

func TestTransactionsBeginImmediate(t *testing.T) {
	path := databasePath(t)
	store := createStore(t, path)
	transaction, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	query := url.Values{
		"mode":          []string{"rw"},
		"_busy_timeout": []string{"1"},
		"_txlock":       []string{"immediate"},
	}
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: query.Encode()}).String()
	other, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	other.SetMaxOpenConns(1)
	second, err := other.BeginTx(context.Background(), nil)
	if err == nil {
		_ = second.Rollback()
		t.Fatal("second immediate transaction acquired the write reservation")
	}
	var sqliteError sqlite3.Error
	if !errors.As(err, &sqliteError) || (sqliteError.Code != sqlite3.ErrBusy && sqliteError.Code != sqlite3.ErrLocked) {
		t.Fatalf("second immediate transaction error=%v", err)
	}
	if classified := classifySQLite("begin competing transaction", err); !errors.Is(classified, ErrBusy) {
		t.Fatalf("busy error classification=%v", classified)
	}
}

func TestOpenReadsExistingMetadata(t *testing.T) {
	path := databasePath(t)
	created := createStore(t, path)
	want := created.Metadata()
	if err := created.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	if opened.Metadata() != want {
		t.Fatalf("opened metadata=%+v, want %+v", opened.Metadata(), want)
	}
}

func TestOpenMigratesRevisionOne(t *testing.T) {
	path := databasePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := connect(context.Background(), path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := initialize(context.Background(), database, "4.0.0-test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if metadata.DatabaseRevision != InitialRevision {
		t.Fatalf("bootstrap revision=%d", metadata.DatabaseRevision)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Metadata().DatabaseRevision != CurrentRevision {
		t.Fatalf("migrated revision=%d, want %d", store.Metadata().DatabaseRevision, CurrentRevision)
	}
	var count int
	if err := store.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'instances'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("instances migration count=%d err=%v", count, err)
	}
}

func TestOpenMigratesRevisionFourLeaseUniqueness(t *testing.T) {
	store, instance := storeWithInstance(t)
	path := store.Path()
	if _, err := store.db.Exec("DROP INDEX client_leases_network_address"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 20, 13, 0, 0, 0, time.UTC)
	clients := []ClientState{
		{Client: domain.Client{ID: "31313131-3131-4313-8313-313131313131", Name: "old-lease", Status: domain.ClientActive}, CreatedAt: now, Assignment: &AddressAssignment{ID: "32323232-3232-4323-8323-323232323232", NetworkID: instance.NetworkID, Kind: "dynamic", Status: AssignmentActive, CreatedAt: now, UpdatedAt: now}},
		{Client: domain.Client{ID: "33333333-3333-4333-8333-333333333334", Name: "new-lease", Status: domain.ClientActive}, CreatedAt: now, Assignment: &AddressAssignment{ID: "34343434-3434-4343-8343-343434343434", NetworkID: instance.NetworkID, Kind: "dynamic", Status: AssignmentActive, CreatedAt: now, UpdatedAt: now}},
	}
	for _, client := range clients {
		if err := store.CreateClient(context.Background(), instance.ID, client); err != nil {
			t.Fatal(err)
		}
	}
	for index, client := range clients {
		if _, err := store.db.Exec(`
INSERT INTO client_leases(client_id, network_id, family, address, updated_at)
VALUES(?, ?, 4, ?, ?)`, client.Client.ID, instance.NetworkID, []byte{10, 42, 0, 200}, formatTime(now.Add(time.Duration(index)*time.Minute))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.db.Exec("UPDATE schema_metadata SET database_revision = 4 WHERE singleton = 1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if opened.Metadata().DatabaseRevision != CurrentRevision {
		t.Fatalf("migrated revision=%d, want %d", opened.Metadata().DatabaseRevision, CurrentRevision)
	}
	var count int
	if err := opened.db.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'client_leases_network_address'").Scan(&count); err != nil || count != 1 {
		t.Fatalf("lease uniqueness index count=%d err=%v", count, err)
	}
	var survivor string
	if err := opened.db.QueryRow("SELECT client_id FROM client_leases").Scan(&survivor); err != nil || survivor != clients[1].Client.ID {
		t.Fatalf("deduplicated lease survivor=%q err=%v", survivor, err)
	}
}

func TestOpenMigratesRevisionSevenWithoutLosingAddressState(t *testing.T) {
	path := databasePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := connect(context.Background(), path, "rwc")
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := initialize(context.Background(), database, "4.0.0-test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range migrations {
		if step.revision > 7 {
			break
		}
		transaction, err := database.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := step.apply(context.Background(), transaction); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if _, err := transaction.Exec("UPDATE schema_metadata SET database_revision = ? WHERE singleton = 1", step.revision); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
		metadata.DatabaseRevision = step.revision
	}
	legacy := &Store{db: database, path: path, metadata: metadata}
	instance := initialInstance(t)
	if err := legacy.CreateInstance(context.Background(), instance); err != nil {
		t.Fatal(err)
	}
	instance, err = legacy.LoadInstance(context.Background(), instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	address, _ := domain.ParseAddress("10.42.0.10")
	client := ClientState{Client: domain.Client{ID: "71717171-7171-4717-8717-717171717171", Name: "legacy", Status: domain.ClientActive}, CreatedAt: now, Assignment: &AddressAssignment{ID: "72727272-7272-4727-8727-727272727272", NetworkID: instance.NetworkID, Kind: "static", Address: &address, Status: AssignmentActive, CreatedAt: now, UpdatedAt: now}}
	if err := legacy.CreateClient(context.Background(), instance.ID, client); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	opened, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	loaded, err := opened.LoadClient(context.Background(), instance.ID, client.Client.ID)
	if err != nil || loaded.Assignment == nil || loaded.Assignment.Address.String() != "10.42.0.10" {
		t.Fatalf("migrated client=%+v err=%v", loaded, err)
	}
	if opened.Metadata().DatabaseRevision != CurrentRevision {
		t.Fatalf("revision=%d", opened.Metadata().DatabaseRevision)
	}
}

func TestCreateNeverOverwritesExistingDatabase(t *testing.T) {
	path := databasePath(t)
	store := createStore(t, path)
	want := store.Metadata()
	if _, err := Create(context.Background(), path, "replacement"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate create error=%v", err)
	}
	if store.Metadata() != want {
		t.Fatal("duplicate create changed the open store")
	}
}

func TestOpenMissingDoesNotCreateDatabase(t *testing.T) {
	path := databasePath(t)
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrMissing) {
		t.Fatalf("missing database error=%v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("open created missing database: %v", err)
	}
}

func TestOpenRejectsUnsafeFileTypeAndMode(t *testing.T) {
	t.Run("mode", func(t *testing.T) {
		path := databasePath(t)
		store := createStore(t, path)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), path); !errors.Is(err, ErrPermission) {
			t.Fatalf("unsafe mode error=%v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		target := databasePath(t)
		store := createStore(t, target)
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(filepath.Dir(target), "linked.db")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), link); !errors.Is(err, ErrPermission) {
			t.Fatalf("symlink error=%v", err)
		}
	})
}

func TestOpenClassifiesCorruptAndUninitializedFiles(t *testing.T) {
	t.Run("corrupt", func(t *testing.T) {
		path := databasePath(t)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("not a SQLite database"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), path); !errors.Is(err, ErrCorrupt) {
			t.Fatalf("corrupt database error=%v", err)
		}
	})
	t.Run("uninitialized", func(t *testing.T) {
		path := databasePath(t)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), path); !errors.Is(err, ErrSchema) {
			t.Fatalf("uninitialized database error=%v", err)
		}
	})
}

func TestIntegrityCheckDetectsForeignKeyDamage(t *testing.T) {
	path := databasePath(t)
	store := createStore(t, path)
	if _, err := store.db.Exec("PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`
INSERT INTO client_leases(client_id, network_id, family, address, updated_at)
VALUES('missing-client', 'missing-network', 4, ?, '2026-07-20T00:00:00Z')`, []byte{10, 42, 0, 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
	if err := store.IntegrityCheck(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("foreign key damage error=%v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("open foreign-key-damaged database error=%v", err)
	}
}

func TestOpenRejectsIncompleteAuthoritySchema(t *testing.T) {
	tests := []struct {
		name      string
		statement string
	}{
		{name: "missing-table", statement: "DROP TABLE operations"},
		{name: "missing-unique-index", statement: "DROP INDEX client_leases_network_address"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := databasePath(t)
			store := createStore(t, path)
			if _, err := store.db.Exec(test.statement); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(context.Background(), path); !errors.Is(err, ErrSchema) {
				t.Fatalf("incomplete schema error=%v", err)
			}
		})
	}
}

func TestOpenRejectsUnsupportedSchemaAndRevision(t *testing.T) {
	tests := []struct {
		name   string
		column string
		value  int
		want   error
	}{
		{"older-data-schema", "data_schema", 3, ErrUnsupportedSchema},
		{"newer-data-schema", "data_schema", 5, ErrUnsupportedSchema},
		{"newer-revision", "database_revision", CurrentRevision + 1, ErrUnsupportedRevision},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := databasePath(t)
			store := createStore(t, path)
			if _, err := store.db.Exec("UPDATE schema_metadata SET "+test.column+" = ? WHERE singleton = 1", test.value); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(context.Background(), path); !errors.Is(err, test.want) {
				t.Fatalf("unsupported state error=%v, want %v", err, test.want)
			}
		})
	}
}

func TestLifecycleRejectsInvalidInputs(t *testing.T) {
	if _, err := Create(context.Background(), "relative.db", "4.0.0"); err == nil {
		t.Fatal("relative database path was accepted")
	}
	path := databasePath(t)
	if _, err := Create(context.Background(), path, ""); err == nil {
		t.Fatal("empty created version was accepted")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid create left a database behind: %v", err)
	}
}
