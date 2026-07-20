package runtime

import (
	"strings"
	"testing"
)

func TestTranslateIdentitiesUsesShortAndFullIDs(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	line := ">CLIENT:CONNECT," + id
	if got := TranslateIdentities(line, map[string]string{id: "laptop"}, false); !strings.Contains(got, "laptop [111111111111]") {
		t.Fatalf("short translation=%q", got)
	}
	if got := TranslateIdentities(line, map[string]string{id: "laptop"}, true); !strings.Contains(got, "laptop ["+id+"]") {
		t.Fatalf("full translation=%q", got)
	}
}

func TestParseAndFormatEvent(t *testing.T) {
	id := "11111111-1111-4111-8111-111111111111"
	value, err := ParseEvent(`{"timestamp":"2026-07-20T00:00:00Z","event":"client_connection","operation":"connect","outcome":"applied","client_id":"` + id + `","client_name":"laptop","bytes_sent":12}`)
	if err != nil {
		t.Fatal(err)
	}
	if text := value.Text(false); !strings.Contains(text, "laptop [111111111111]") || !strings.Contains(text, "bytes_sent=12") {
		t.Fatalf("event text=%q", text)
	}
	encoded, err := value.JSON()
	if err != nil || !strings.Contains(string(encoded), `"client_id":"`+id+`"`) {
		t.Fatalf("event JSON=%q err=%v", encoded, err)
	}
	if _, err := ParseEvent(`{"timestamp":"now"}`); err == nil {
		t.Fatal("malformed event was accepted")
	}
	if _, err := ParseEvent(`{"timestamp":"now","event":"x","operation":"x","outcome":"x"} {}`); err == nil {
		t.Fatal("multiple event values were accepted")
	}
}
