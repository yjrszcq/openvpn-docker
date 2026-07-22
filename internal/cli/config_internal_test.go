package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
)

func TestDataDirectoryEmpty(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	empty, err := dataDirectoryEmpty(missing)
	if err != nil || !empty {
		t.Fatalf("missing directory empty=%v err=%v", empty, err)
	}
	directory := t.TempDir()
	empty, err = dataDirectoryEmpty(directory)
	if err != nil || !empty {
		t.Fatalf("empty directory empty=%v err=%v", empty, err)
	}
	for _, name := range []string{".ovpn-data.lock", ".ovpn-runtime.lock"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	empty, err = dataDirectoryEmpty(directory)
	if err != nil || !empty {
		t.Fatalf("lock-only directory empty=%v err=%v", empty, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "schema-version"), []byte("3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty, err = dataDirectoryEmpty(directory)
	if err != nil || empty {
		t.Fatalf("legacy directory empty=%v err=%v", empty, err)
	}
}

func TestConfigurationApplyHumanOutputListsEveryProfile(t *testing.T) {
	result := configurationservice.ApplyResult{
		Applied:     true,
		OperationID: "90909090-9090-4909-8909-909090909090",
		Activation: configurationservice.ActivationReport{
			RestartRequired: true,
			ProfileRedistribution: []configurationservice.ClientRef{
				{ID: "91919191-9191-4919-8919-919191919191", Name: "alpha"},
				{ID: "92929292-9292-4929-8929-929292929292", Name: "beta"},
			},
		},
		Plan: configurationservice.Plan{Configuration: configservice.Plan{TargetRevision: 4}},
	}
	var output bytes.Buffer
	writeConfigurationApplyResult(&output, result)
	for _, expected := range []string{
		"Applied configuration revision 4",
		"Restart required: yes",
		"offline apply did not restart OpenVPN",
		"Profiles to redistribute: 2",
		"alpha [91919191-9191-4919-8919-919191919191]",
		"beta [92929292-9292-4929-8929-929292929292]",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Errorf("output is missing %q:\n%s", expected, output.String())
		}
	}
}

func TestConfigurationApplyHumanOutputReportsNoProfiles(t *testing.T) {
	result := configurationservice.ApplyResult{Applied: true, Activation: configurationservice.ActivationReport{RestartRequired: true, ProfileRedistribution: []configurationservice.ClientRef{}}, Plan: configurationservice.Plan{Configuration: configservice.Plan{TargetRevision: 2}}}
	var output bytes.Buffer
	writeConfigurationApplyResult(&output, result)
	if !strings.Contains(output.String(), "Profiles to redistribute: none.") {
		t.Fatalf("output=%q", output.String())
	}
}

func TestConfigurationApplyHumanOutputReportsRuntimeRestart(t *testing.T) {
	result := configurationservice.ApplyResult{Applied: true, Activation: configurationservice.ActivationReport{RuntimeRestarted: true, ProfileRedistribution: []configurationservice.ClientRef{}}, Plan: configurationservice.Plan{Configuration: configservice.Plan{TargetRevision: 2}}}
	var output bytes.Buffer
	writeConfigurationApplyResult(&output, result)
	if !strings.Contains(output.String(), "Runtime restarted: yes.") || strings.Contains(output.String(), "Restart required: yes") {
		t.Fatalf("output=%q", output.String())
	}
}
