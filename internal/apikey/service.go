// Package apikey manages bearer credentials for the local REST API.
package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

const tokenPrefix = "ovpn_v1"

var (
	ErrNotFound        = errors.New("API key not found")
	ErrConflict        = errors.New("API key conflicts with existing state")
	ErrUnauthenticated = errors.New("API key authentication failed")
)

type Key struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type Record struct {
	Key
	Digest [sha256.Size]byte `json:"-"`
}

type Selector struct {
	Name     string
	IDPrefix string
}

type CreateResult struct {
	Version     int    `json:"version"`
	OperationID string `json:"operation_id"`
	Key         Key    `json:"key"`
	Secret      string `json:"secret"`
}

type DeleteResult struct {
	Version     int       `json:"version"`
	OperationID string    `json:"operation_id"`
	Key         Key       `json:"key"`
	DeletedAt   time.Time `json:"deleted_at"`
}

type ListResult struct {
	Version int   `json:"version"`
	Keys    []Key `json:"keys"`
}

type Store interface {
	CreateAPIKey(context.Context, string, Record, string) error
	ListAPIKeys(context.Context, string) ([]Record, error)
	LoadAPIKey(context.Context, string, string) (Record, error)
	DeleteAPIKey(context.Context, string, string, string, time.Time) (Record, error)
}

type Service struct {
	store      Store
	instanceID string
	random     io.Reader
	now        func() time.Time
}

func NewService(store Store, instanceID string) (*Service, error) {
	if store == nil || !domain.ValidUUID(instanceID) {
		return nil, fmt.Errorf("API key service requires a store and instance UUID")
	}
	return &Service{store: store, instanceID: instanceID, random: rand.Reader, now: time.Now}, nil
}

func (service *Service) Create(ctx context.Context, name string) (CreateResult, error) {
	if !domain.ValidClientName(name) {
		return CreateResult{}, fmt.Errorf("invalid API key name %q", name)
	}
	id, err := domain.GenerateUUID()
	if err != nil {
		return CreateResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return CreateResult{}, err
	}
	secret := make([]byte, 32)
	if _, err := io.ReadFull(service.random, secret); err != nil {
		return CreateResult{}, fmt.Errorf("generate API key secret: %w", err)
	}
	token := tokenPrefix + "." + id + "." + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(token))
	key := Key{ID: id, Name: name, CreatedAt: service.now().UTC().Truncate(time.Second)}
	if err := service.store.CreateAPIKey(ctx, service.instanceID, Record{Key: key, Digest: digest}, operationID); err != nil {
		return CreateResult{}, err
	}
	return CreateResult{Version: 1, OperationID: operationID, Key: key, Secret: token}, nil
}

func (service *Service) List(ctx context.Context) (ListResult, error) {
	records, err := service.store.ListAPIKeys(ctx, service.instanceID)
	if err != nil {
		return ListResult{}, err
	}
	keys := make([]Key, len(records))
	for index := range records {
		keys[index] = records[index].Key
	}
	return ListResult{Version: 1, Keys: keys}, nil
}

func (service *Service) Select(ctx context.Context, selector Selector) (Record, error) {
	if selector.Name != "" && selector.IDPrefix != "" {
		return Record{}, fmt.Errorf("API key selector must use either name or ID")
	}
	if selector.Name != "" {
		if !domain.ValidClientName(selector.Name) {
			return Record{}, fmt.Errorf("invalid API key name")
		}
		records, err := service.store.ListAPIKeys(ctx, service.instanceID)
		if err != nil {
			return Record{}, err
		}
		for _, record := range records {
			if record.Name == selector.Name {
				return record, nil
			}
		}
		return Record{}, ErrNotFound
	}
	prefix := strings.ToLower(strings.ReplaceAll(selector.IDPrefix, "-", ""))
	if len(prefix) < 8 || len(prefix) > 32 {
		return Record{}, fmt.Errorf("API key ID prefix must contain 8 to 32 hexadecimal characters")
	}
	for _, value := range prefix {
		if !strings.ContainsRune("0123456789abcdef", value) {
			return Record{}, fmt.Errorf("API key ID prefix must be hexadecimal")
		}
	}
	records, err := service.store.ListAPIKeys(ctx, service.instanceID)
	if err != nil {
		return Record{}, err
	}
	var match *Record
	for index := range records {
		if strings.HasPrefix(strings.ReplaceAll(records[index].ID, "-", ""), prefix) {
			if match != nil {
				return Record{}, fmt.Errorf("API key ID prefix is ambiguous")
			}
			value := records[index]
			match = &value
		}
	}
	if match == nil {
		return Record{}, ErrNotFound
	}
	return *match, nil
}

func (service *Service) Delete(ctx context.Context, selector Selector) (DeleteResult, error) {
	record, err := service.Select(ctx, selector)
	if err != nil {
		return DeleteResult{}, err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return DeleteResult{}, err
	}
	deletedAt := service.now().UTC().Truncate(time.Second)
	deleted, err := service.store.DeleteAPIKey(ctx, service.instanceID, record.ID, operationID, deletedAt)
	if err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{Version: 1, OperationID: operationID, Key: deleted.Key, DeletedAt: deletedAt}, nil
}

func (service *Service) Authenticate(ctx context.Context, token string) (Key, error) {
	id, digest, ok := parseToken(token)
	if !ok {
		return Key{}, ErrUnauthenticated
	}
	record, err := service.store.LoadAPIKey(ctx, service.instanceID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Key{}, ErrUnauthenticated
		}
		return Key{}, fmt.Errorf("load API key authentication state: %w", err)
	}
	if subtle.ConstantTimeCompare(digest[:], record.Digest[:]) != 1 {
		return Key{}, ErrUnauthenticated
	}
	return record.Key, nil
}

func parseToken(token string) (string, [sha256.Size]byte, bool) {
	digest := sha256.Sum256([]byte(token))
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenPrefix || !domain.ValidUUID(parts[1]) {
		return "", digest, false
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != 32 || base64.RawURLEncoding.EncodeToString(secret) != parts[2] {
		return "", digest, false
	}
	return parts[1], digest, true
}
