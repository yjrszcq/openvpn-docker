package schema3_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

type behaviorContract struct {
	ContractVersion int `json:"contract_version"`
	Source          struct {
		ImageVersion string `json:"image_version"`
		DataSchema   int    `json:"data_schema"`
	} `json:"source"`
	Defaults     map[string]string `json:"defaults"`
	StateClasses []string          `json:"state_classes"`
	Commands     []struct {
		V3          string `json:"v3"`
		V4          string `json:"v4"`
		Disposition string `json:"disposition"`
	} `json:"commands"`
	RenderCases []struct {
		Family   string `json:"family"`
		Protocol string `json:"protocol"`
		Endpoint string `json:"endpoint"`
	} `json:"render_cases"`
	SourcePaths []string `json:"source_paths"`
}

type testInventory struct {
	ContractVersion int `json:"contract_version"`
	Entries         []struct {
		Path           string `json:"path"`
		Classification string `json:"classification"`
		Replacement    string `json:"replacement"`
		Area           string `json:"area"`
	} `json:"entries"`
}

func decodeJSON[T any](t *testing.T, path string) T {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	return value
}

func TestBehaviorContractBaseline(t *testing.T) {
	contract := decodeJSON[behaviorContract](t, "behavior.json")
	if contract.ContractVersion != 1 || contract.Source.ImageVersion != "3.2.0" || contract.Source.DataSchema != 3 {
		t.Fatalf("unexpected source baseline: version=%d image=%q schema=%d", contract.ContractVersion, contract.Source.ImageVersion, contract.Source.DataSchema)
	}
	if contract.Defaults["OVPN_NETWORK"] != "10.8.0.0/24" || contract.Defaults["OVPN_DYNAMIC_POOL_SIZE"] == "" {
		t.Fatal("configuration defaults are incomplete")
	}
	if len(contract.Commands) != 30 {
		t.Fatalf("command inventory has %d entries, want 30", len(contract.Commands))
	}
	if len(contract.StateClasses) != 7 || len(contract.RenderCases) != 10 {
		t.Fatalf("incomplete state/render contract: states=%d renders=%d", len(contract.StateClasses), len(contract.RenderCases))
	}

	seen := make(map[string]bool, len(contract.Commands))
	validDisposition := map[string]bool{"preserve": true, "redesign": true, "fold": true, "retire": true}
	for _, command := range contract.Commands {
		if command.V3 == "" || command.V4 == "" || !validDisposition[command.Disposition] {
			t.Fatalf("invalid command contract: %+v", command)
		}
		if seen[command.V3] {
			t.Fatalf("duplicate v3 command: %s", command.V3)
		}
		seen[command.V3] = true
	}

	seenPaths := make(map[string]bool, len(contract.SourcePaths))
	for _, path := range contract.SourcePaths {
		if path == "" || seenPaths[path] {
			t.Errorf("invalid or duplicate historical source path %q", path)
		}
		seenPaths[path] = true
	}
}

func TestInventoryClassifiesEveryExistingTest(t *testing.T) {
	inventory := decodeJSON[testInventory](t, "test-inventory.json")
	if inventory.ContractVersion != 1 {
		t.Fatalf("unexpected inventory contract version: %d", inventory.ContractVersion)
	}

	repositoryRoot := filepath.Join("..", "..")
	patterns := []string{
		"tests/smoke/shell/*.sh",
		"tests/smoke/container/*.sh",
	}
	var actual []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(repositoryRoot, pattern))
		if err != nil {
			t.Fatalf("glob %s: %v", pattern, err)
		}
		for _, match := range matches {
			relative, err := filepath.Rel(repositoryRoot, match)
			if err != nil {
				t.Fatalf("make %s relative: %v", match, err)
			}
			actual = append(actual, filepath.ToSlash(relative))
		}
	}

	listed := make([]string, 0, len(inventory.Entries))
	seen := make(map[string]bool, len(inventory.Entries))
	for _, entry := range inventory.Entries {
		if entry.Path == "" || entry.Classification == "" || entry.Replacement == "" || entry.Area == "" {
			t.Fatalf("incomplete test inventory entry: %+v", entry)
		}
		if seen[entry.Path] {
			t.Fatalf("duplicate test inventory entry: %s", entry.Path)
		}
		seen[entry.Path] = true
		listed = append(listed, entry.Path)
	}

	sort.Strings(actual)
	sort.Strings(listed)
	if len(actual) != len(listed) {
		t.Fatalf("test inventory count=%d, existing tests=%d", len(listed), len(actual))
	}
	for index := range actual {
		if actual[index] != listed[index] {
			t.Fatalf("test inventory differs at %d: listed=%q actual=%q", index, listed[index], actual[index])
		}
	}
}
