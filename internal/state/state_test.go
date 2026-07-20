package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func TestScanClassifiesEmptyAndMissingAuthority(t *testing.T) {
	empty := t.TempDir()
	report := Scan(context.Background(), Options{DataDir: empty})
	if report.State != Empty || report.IssueCount != 0 || report.Issues == nil {
		t.Fatalf("empty report=%+v", report)
	}
	if err := os.WriteFile(filepath.Join(empty, ".ovpn-data.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	report = Scan(context.Background(), Options{DataDir: empty})
	if report.State != Empty {
		t.Fatalf("lock-only directory report=%+v", report)
	}
	nonempty := t.TempDir()
	if err := os.WriteFile(filepath.Join(nonempty, "legacy-state"), []byte("present\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	report = Scan(context.Background(), Options{DataDir: nonempty})
	if report.State != Critical || report.IssueCount != 1 || report.Issues[0].ID != "SQLITE_MISSING" || report.Issues[0].Action != "RESTORE_BACKUP" {
		t.Fatalf("missing database report=%+v", report)
	}
}

func TestMissingMetadataForPresentAuthorityFileIsRepairable(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "pki", "private")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "ca.key"), []byte("present\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	local, err := artifact.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	report := Report{State: Healthy, Issues: []Issue{}}
	scanArtifactSet(context.Background(), &report, local, nil, "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	for _, issue := range report.Issues {
		if issue.ArtifactKind == "ca-key" {
			if issue.Severity != SeverityRepairable || issue.Action != "REGISTER_VERIFIED_ARTIFACT" {
				t.Fatalf("CA key issue=%+v", issue)
			}
			return
		}
	}
	t.Fatal("missing CA key metadata was not reported")
}

func TestClassificationUsesHighestSeverityAndStableOrder(t *testing.T) {
	report := Report{State: Healthy, Issues: []Issue{
		{ID: "Z", Severity: SeverityRepairable},
		{ID: "B", Severity: SeverityCritical},
		{ID: "A", Severity: SeverityCritical},
	}}.finish()
	if report.State != Critical || report.IssueCount != 3 || report.Issues[0].ID != "A" || report.Issues[2].ID != "Z" {
		t.Fatalf("report=%+v", report)
	}
}

func TestInstanceScannerRejectsDuplicateActiveArtifactKinds(t *testing.T) {
	root := t.TempDir()
	local, err := artifact.NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	owner := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	metadata := []storesqlite.ArtifactMetadata{
		{OwnerKind: "instance", OwnerID: owner, Kind: "ca-cert", Key: "pki/ca.crt", Status: storesqlite.ArtifactActive},
		{OwnerKind: "instance", OwnerID: owner, Kind: "ca-cert", Key: "backup/ca.crt", Status: storesqlite.ArtifactActive},
	}
	report := Report{State: Healthy, Issues: []Issue{}}
	scanArtifactSet(context.Background(), &report, local, metadata, owner)
	for _, issue := range report.Issues {
		if issue.ID == "ARTIFACT_METADATA_DUPLICATE" && issue.Severity == SeverityCritical {
			return
		}
	}
	t.Fatalf("duplicate artifact issue missing: %+v", report.Issues)
}
