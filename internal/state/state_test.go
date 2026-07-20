package state

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanClassifiesEmptyAndMissingAuthority(t *testing.T) {
	empty := t.TempDir()
	report := Scan(context.Background(), Options{DataDir: empty})
	if report.State != Empty || report.IssueCount != 0 || report.Issues == nil {
		t.Fatalf("empty report=%+v", report)
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
