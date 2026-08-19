package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
)

func TestAPIKeyLifecycleAndAudit(t *testing.T) {
	store, instance := storeWithInstance(t)
	service, err := apikey.NewService(store, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context.Background(), "frontend")
	if err != nil {
		t.Fatal(err)
	}
	var storedDigest []byte
	if err := store.db.QueryRow("SELECT secret_digest FROM api_keys WHERE id = ?", created.Key.ID).Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	if len(storedDigest) != 32 || strings.Contains(string(storedDigest), created.Secret) {
		t.Fatalf("unsafe stored digest length=%d", len(storedDigest))
	}
	if _, err := service.Create(context.Background(), "frontend"); !errors.Is(err, apikey.ErrConflict) {
		t.Fatalf("duplicate key error=%v", err)
	}
	listed, err := service.List(context.Background())
	if err != nil || len(listed.Keys) != 1 || listed.Keys[0].ID != created.Key.ID {
		t.Fatalf("listed=%+v err=%v", listed, err)
	}
	if _, err := service.Delete(context.Background(), apikey.Selector{Name: "frontend"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(context.Background(), created.Secret); !errors.Is(err, apikey.ErrUnauthenticated) {
		t.Fatalf("deleted authentication error=%v", err)
	}
	events, err := store.AuditEvents(context.Background(), instance.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if events[len(events)-2].Type != "api_key.created" || events[len(events)-1].Type != "api_key.deleted" {
		t.Fatalf("unexpected API key audit: %+v", events)
	}
	for _, event := range events[len(events)-2:] {
		if strings.Contains(string(event.Payload), created.Secret) || strings.Contains(string(event.Payload), "secret_digest") {
			t.Fatalf("audit leaked credential: %s", event.Payload)
		}
	}
}
