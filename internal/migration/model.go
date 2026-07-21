// Package migration reads and migrates the legacy schema 3 control-plane state.
package migration

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
)

const LegacySchema = 3

var (
	ErrNeedsShellUpgrade = errors.New("legacy schema must first be upgraded with the sh-ver image")
	ErrUnsupportedSource = errors.New("migration source schema is unsupported")
	ErrInvalidSource     = errors.New("legacy migration source is invalid")
)

type SourceStatus string

const (
	SourceSchema3  SourceStatus = "schema-3"
	SourceLegacy   SourceStatus = "legacy-upgrade-required"
	SourceNewer    SourceStatus = "newer"
	SourceUnknown  SourceStatus = "unknown"
	SourceConflict SourceStatus = "conflict"
)

type Probe struct {
	Status         SourceStatus `json:"status"`
	ProjectVersion int          `json:"project_version,omitempty"`
	FileVersion    int          `json:"file_version,omitempty"`
}

type Instance struct {
	ID            string
	InitializedAt time.Time
	ServerName    string
	DataDir       string
	CAFingerprint [32]byte
}

type Client struct {
	Client      domain.Client
	Address     *domain.Address
	Certificate *pki.CertificateInfo
	ProfileKey  string
}

type AuditEvent struct {
	Timestamp    time.Time `json:"timestamp"`
	Event        string    `json:"event"`
	Operation    string    `json:"operation,omitempty"`
	Outcome      string    `json:"outcome"`
	ClientID     *string   `json:"client_id"`
	ClientName   *string   `json:"client_name"`
	OldName      string    `json:"old_name,omitempty"`
	Legacy       bool      `json:"legacy"`
	SourceSchema int       `json:"source_schema,omitempty"`
	Raw          []byte    `json:"-"`
}

type Lease struct {
	ClientID  string
	Address   domain.Address
	UpdatedAt time.Time
	Import    bool
	Reason    string
}

type Artifact struct {
	OwnerKind              string
	OwnerID                string
	Kind                   string
	Key                    string
	Digest                 [32]byte
	Mode                   uint32
	CertificateSerial      string
	CertificateFingerprint []byte
}

type Issue struct {
	Code   string `json:"code"`
	Path   string `json:"path"`
	Detail string `json:"detail"`
}

type Source struct {
	Root               string
	Probe              Probe
	Config             domain.Config
	Instance           Instance
	Clients            []Client
	Audit              []AuditEvent
	Leases             []Lease
	Artifacts          []Artifact
	Repairs            []Issue
	CanonicalClientIPs bool
}

func invalid(path, format string, values ...any) error {
	detail := fmt.Sprintf(format, values...)
	if path != "" {
		detail = path + ": " + detail
	}
	return fmt.Errorf("%w: %s", ErrInvalidSource, detail)
}

func digest(data []byte) [32]byte { return sha256.Sum256(data) }
