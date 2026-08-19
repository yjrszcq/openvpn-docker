package sqlite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/auditactor"
)

func TestAuditActorIsAddedWithoutReplacingPayload(t *testing.T) {
	store, instance := storeWithInstance(t)
	service, err := apikey.NewService(store, instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := auditactor.With(context.Background(), auditactor.Actor{Kind: "api-key", ID: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"})
	if _, err := service.Create(ctx, "frontend"); err != nil {
		t.Fatal(err)
	}
	events, err := store.AuditEvents(context.Background(), instance.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[len(events)-1].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["actor_kind"] != "api-key" || payload["actor_id"] != "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb" || payload["name"] != "frontend" {
		t.Fatalf("payload=%v", payload)
	}
}
