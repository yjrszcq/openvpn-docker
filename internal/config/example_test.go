package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRootExampleConfigurationIsStrictAndComplete(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "config.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := Parse(data)
	if err != nil {
		t.Fatalf("parse config.example.yaml: %v", err)
	}
	if value.Endpoint != "vpn.example.com" || value.IPv4.Network.String() != "10.42.0.0/24" || value.IPv4.DynamicPoolSize != 64 || value.Port != 1194 {
		t.Fatalf("unexpected example normalization: %+v", value)
	}
}
