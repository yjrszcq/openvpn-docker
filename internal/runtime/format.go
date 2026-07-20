package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

var uuidInText = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}`)

func TranslateIdentities(line string, identities map[string]string, fullID bool) string {
	return uuidInText.ReplaceAllStringFunc(line, func(id string) string {
		name, ok := identities[id]
		if !ok {
			return id
		}
		return fmt.Sprintf("%s [%s]", name, displayID(id, fullID))
	})
}

type Event map[string]any

func ParseEvent(line string) (Event, error) {
	decoder := json.NewDecoder(strings.NewReader(line))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid structured event: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("invalid structured event: multiple JSON values")
		}
		return nil, fmt.Errorf("invalid structured event: %w", err)
	}
	if value == nil {
		return nil, fmt.Errorf("invalid structured event: event is not an object")
	}
	for _, field := range []string{"timestamp", "event", "operation", "outcome"} {
		text, ok := value[field].(string)
		if !ok || text == "" {
			return nil, fmt.Errorf("invalid structured event: %s is missing", field)
		}
	}
	for _, field := range []string{"client_id", "client_name"} {
		if value[field] != nil {
			if _, ok := value[field].(string); !ok {
				return nil, fmt.Errorf("invalid structured event: %s is not a string or null", field)
			}
		}
	}
	return Event(value), nil
}

func (event Event) JSON() ([]byte, error) { return json.Marshal(map[string]any(event)) }

func (event Event) Text(fullID bool) string {
	clientID, _ := event["client_id"].(string)
	clientName, _ := event["client_name"].(string)
	identity := "-"
	if clientID != "" && clientName != "" {
		identity = fmt.Sprintf("%s [%s]", clientName, displayID(clientID, fullID))
	} else if clientID != "" {
		identity = displayID(clientID, fullID)
	} else if clientName != "" {
		identity = clientName
	}
	core := map[string]bool{"timestamp": true, "event": true, "operation": true, "outcome": true, "client_id": true, "client_name": true}
	keys := make([]string, 0)
	for key := range event {
		if !core[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := []string{fmt.Sprint(event["timestamp"]), fmt.Sprint(event["event"]), fmt.Sprint(event["operation"]), fmt.Sprint(event["outcome"]), identity}
	for _, key := range keys {
		encoded, _ := json.Marshal(event[key])
		parts = append(parts, key+"="+string(encoded))
	}
	return strings.Join(parts, " ")
}

func displayID(id string, full bool) string {
	location := uuidInText.FindStringIndex(id)
	if full || location == nil || location[0] != 0 || location[1] != len(id) {
		return id
	}
	return strings.ReplaceAll(id, "-", "")[:12]
}
