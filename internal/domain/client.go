package domain

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

var (
	uuidPattern       = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	clientNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

// ClientStatus is a durable client lifecycle state.
type ClientStatus string

const (
	ClientActive  ClientStatus = "active"
	ClientRevoked ClientStatus = "revoked"
	ClientDeleted ClientStatus = "deleted"
)

// Client identifies a logical client independently from certificate generations.
type Client struct {
	ID     string
	Name   string
	Status ClientStatus
}

// NewClient validates the stable identity fields used by state adapters.
func NewClient(id, name string, status ClientStatus) (Client, error) {
	if !ValidUUID(id) {
		return Client{}, fmt.Errorf("invalid client UUID %q", id)
	}
	if !clientNamePattern.MatchString(name) || ValidUUID(name) {
		return Client{}, fmt.Errorf("invalid client name %q", name)
	}
	switch status {
	case ClientActive, ClientRevoked, ClientDeleted:
	default:
		return Client{}, fmt.Errorf("invalid client status %q", status)
	}
	return Client{ID: id, Name: name, Status: status}, nil
}

// ValidUUID reports whether value is the canonical application UUID format.
func ValidUUID(value string) bool { return uuidPattern.MatchString(value) }

// GenerateUUID creates a canonical random UUIDv4 for application-owned IDs.
func GenerateUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
