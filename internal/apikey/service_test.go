package apikey

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

type memoryStore struct {
	records map[string]Record
}

func (store *memoryStore) CreateAPIKey(_ context.Context, _ string, record Record, _ string) error {
	for _, existing := range store.records {
		if existing.Name == record.Name {
			return ErrConflict
		}
	}
	store.records[record.ID] = record
	return nil
}

func (store *memoryStore) ListAPIKeys(context.Context, string) ([]Record, error) {
	values := make([]Record, 0, len(store.records))
	for _, value := range store.records {
		values = append(values, value)
	}
	return values, nil
}

func (store *memoryStore) LoadAPIKey(_ context.Context, _ string, id string) (Record, error) {
	value, ok := store.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	return value, nil
}

func (store *memoryStore) DeleteAPIKey(_ context.Context, _, id, _ string, _ time.Time) (Record, error) {
	value, ok := store.records[id]
	if !ok {
		return Record{}, ErrNotFound
	}
	delete(store.records, id)
	return value, nil
}

func TestCreateAuthenticateAndDelete(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	service, err := NewService(store, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	if err != nil {
		t.Fatal(err)
	}
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x42}, 32))
	service.now = func() time.Time { return time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC) }
	created, err := service.Create(context.Background(), "frontend")
	if err != nil {
		t.Fatal(err)
	}
	if created.Secret == "" || created.Key.Name != "frontend" || len(store.records) != 1 {
		t.Fatalf("created=%+v records=%d", created, len(store.records))
	}
	authenticated, err := service.Authenticate(context.Background(), created.Secret)
	if err != nil || authenticated.ID != created.Key.ID {
		t.Fatalf("authenticated=%+v err=%v", authenticated, err)
	}
	if _, err := service.Authenticate(context.Background(), created.Secret+"x"); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("tampered authentication error=%v", err)
	}
	deleted, err := service.Delete(context.Background(), Selector{IDPrefix: created.Key.ID[:8]})
	if err != nil || deleted.Key.ID != created.Key.ID {
		t.Fatalf("deleted=%+v err=%v", deleted, err)
	}
	if _, err := service.Authenticate(context.Background(), created.Secret); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("deleted key authentication error=%v", err)
	}
}

func TestValidationAndConflicts(t *testing.T) {
	store := &memoryStore{records: map[string]Record{}}
	service, _ := NewService(store, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	service.random = bytes.NewReader(bytes.Repeat([]byte{0x11}, 64))
	if _, err := service.Create(context.Background(), "bad name"); err == nil {
		t.Fatal("invalid name was accepted")
	}
	if _, err := service.Create(context.Background(), "frontend"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), "frontend"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate name error=%v", err)
	}
	for _, token := range []string{"", "ovpn_v1.bad.secret", "ovpn_v2.aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.secret"} {
		if _, err := service.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("token %q error=%v", token, err)
		}
	}
}
