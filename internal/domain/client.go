package domain

import (
	"fmt"
	"regexp"
)

var (
	clientIDPattern   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
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
	if !clientIDPattern.MatchString(id) {
		return Client{}, fmt.Errorf("invalid client UUID %q", id)
	}
	if !clientNamePattern.MatchString(name) || clientIDPattern.MatchString(name) {
		return Client{}, fmt.Errorf("invalid client name %q", name)
	}
	switch status {
	case ClientActive, ClientRevoked, ClientDeleted:
	default:
		return Client{}, fmt.Errorf("invalid client status %q", status)
	}
	return Client{ID: id, Name: name, Status: status}, nil
}
