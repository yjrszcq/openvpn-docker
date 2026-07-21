package cli

import (
	"bytes"
	"strings"
	"testing"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
)

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
		"OpenVPN was not reloaded",
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
