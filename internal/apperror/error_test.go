package apperror_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
)

func TestWriteJSONDoesNotExposeCause(t *testing.T) {
	err := apperror.Wrap(apperror.ExitData, "invalid_config", "configuration is invalid", errors.New("secret parser detail"))
	var output bytes.Buffer
	if code := apperror.Write(&output, err, true); code != apperror.ExitData {
		t.Fatalf("exit code = %d, want %d", code, apperror.ExitData)
	}
	if got := output.String(); got != "{\"error\":{\"code\":65,\"kind\":\"invalid_config\",\"message\":\"configuration is invalid\"}}\n" {
		t.Fatalf("unexpected JSON error: %s", got)
	}
	if strings.Contains(output.String(), "secret parser detail") {
		t.Fatal("JSON error exposed its internal cause")
	}
}

func TestUnknownErrorUsesFailureCode(t *testing.T) {
	if code := apperror.Code(errors.New("boom")); code != apperror.ExitFailure {
		t.Fatalf("exit code = %d, want %d", code, apperror.ExitFailure)
	}
}
