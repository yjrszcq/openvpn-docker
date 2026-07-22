package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandOverviewContainsExpandedCommands(t *testing.T) {
	var output bytes.Buffer
	writeCommandOverview(&output)
	text := output.String()
	if !strings.HasPrefix(text, "Usage: ovpn <command> [options]\n\nCommand tree:\n") {
		t.Fatalf("unexpected overview header: %q", text)
	}
	if strings.Contains(text, "Details:") || strings.Contains(text, "Examples:") {
		t.Fatalf("overview is not compact: %q", text)
	}

	var check func(command)
	check = func(current command) {
		for _, child := range current.children {
			entry := child.name + " - " + child.summary + "\n"
			if child.summary == "" || !strings.Contains(text, entry) {
				t.Errorf("overview is missing command summary for %s: %q", child.name, text)
			}
			if len(child.children) > 0 {
				check(child)
				continue
			}
			if child.usage == "" || !strings.Contains(text, "Usage: "+child.usage+"\n") {
				t.Errorf("overview is missing leaf usage for %s: %q", child.name, child.usage)
			}
		}
	}
	check(rootCommand)
}

func TestBareCommandUsesExpandedOverviewAndHelpRemainsDetailed(t *testing.T) {
	var overview, detailed, stderr bytes.Buffer
	if code := Run(nil, &overview, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("bare command code=%d stderr=%q", code, stderr.String())
	}
	if code := Run([]string{"--help"}, &detailed, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("help code=%d stderr=%q", code, stderr.String())
	}
	if overview.String() == detailed.String() || !strings.Contains(detailed.String(), "Details:") {
		t.Fatalf("overview=%q detailed=%q", overview.String(), detailed.String())
	}
}
