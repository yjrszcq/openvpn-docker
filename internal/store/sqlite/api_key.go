package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

func (store *Store) CreateAPIKey(ctx context.Context, instanceID string, record apikey.Record, operationID string) error {
	if err := validateAPIKey(instanceID, record); err != nil || !domain.ValidUUID(operationID) {
		return fmt.Errorf("invalid API key creation")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin API key creation", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO api_keys(id, instance_id, name, secret_digest, created_at)
VALUES(?, ?, ?, ?, ?)`, record.ID, instanceID, record.Name, record.Digest[:], formatTime(record.CreatedAt)); err != nil {
		classified := classifySQLite("insert API key", err)
		if errors.Is(classified, ErrConstraint) {
			return fmt.Errorf("%w: %v", apikey.ErrConflict, classified)
		}
		return classified
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "api_key.created", map[string]any{
		"key_id": record.ID, "name": record.Name, "created_at": formatTime(record.CreatedAt),
	}); err != nil {
		return err
	}
	return classifyCommit(transaction.Commit(), "commit API key creation")
}

func (store *Store) ListAPIKeys(ctx context.Context, instanceID string) ([]apikey.Record, error) {
	if !domain.ValidUUID(instanceID) {
		return nil, fmt.Errorf("invalid instance UUID")
	}
	rows, err := store.db.QueryContext(ctx, `
SELECT id, name, secret_digest, created_at
FROM api_keys WHERE instance_id = ? ORDER BY created_at, id`, instanceID)
	if err != nil {
		return nil, classifySQLite("list API keys", err)
	}
	defer rows.Close()
	values := make([]apikey.Record, 0)
	for rows.Next() {
		value, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *Store) LoadAPIKey(ctx context.Context, instanceID, keyID string) (apikey.Record, error) {
	if !domain.ValidUUID(instanceID) || !domain.ValidUUID(keyID) {
		return apikey.Record{}, apikey.ErrNotFound
	}
	value, err := scanAPIKey(store.db.QueryRowContext(ctx, `
SELECT id, name, secret_digest, created_at
FROM api_keys WHERE instance_id = ? AND id = ?`, instanceID, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return apikey.Record{}, apikey.ErrNotFound
	}
	return value, err
}

func (store *Store) DeleteAPIKey(ctx context.Context, instanceID, keyID, operationID string, deletedAt time.Time) (apikey.Record, error) {
	if !domain.ValidUUID(instanceID) || !domain.ValidUUID(keyID) || !domain.ValidUUID(operationID) || deletedAt.IsZero() {
		return apikey.Record{}, fmt.Errorf("invalid API key deletion")
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return apikey.Record{}, classifySQLite("begin API key deletion", err)
	}
	defer transaction.Rollback()
	record, err := scanAPIKey(transaction.QueryRowContext(ctx, `
SELECT id, name, secret_digest, created_at
FROM api_keys WHERE instance_id = ? AND id = ?`, instanceID, keyID))
	if errors.Is(err, sql.ErrNoRows) {
		return apikey.Record{}, apikey.ErrNotFound
	}
	if err != nil {
		return apikey.Record{}, err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "api_key.deleted", map[string]any{
		"key_id": record.ID, "name": record.Name, "created_at": formatTime(record.CreatedAt), "deleted_at": formatTime(deletedAt),
	}); err != nil {
		return apikey.Record{}, err
	}
	if _, err := transaction.ExecContext(ctx, "DELETE FROM api_keys WHERE instance_id = ? AND id = ?", instanceID, keyID); err != nil {
		return apikey.Record{}, classifySQLite("delete API key", err)
	}
	if err := transaction.Commit(); err != nil {
		return apikey.Record{}, classifySQLite("commit API key deletion", err)
	}
	return record, nil
}

type apiKeyScanner interface {
	Scan(...any) error
}

func scanAPIKey(scanner apiKeyScanner) (apikey.Record, error) {
	var value apikey.Record
	var digest []byte
	var createdAt string
	if err := scanner.Scan(&value.ID, &value.Name, &digest, &createdAt); err != nil {
		return apikey.Record{}, err
	}
	if len(digest) != len(value.Digest) {
		return apikey.Record{}, fmt.Errorf("%w: invalid API key digest", ErrSchema)
	}
	copy(value.Digest[:], digest)
	created, err := parseTime(createdAt)
	if err != nil {
		return apikey.Record{}, err
	}
	value.CreatedAt = created
	if err := validateAPIKeyRecord(value); err != nil {
		return apikey.Record{}, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	return value, nil
}

func validateAPIKey(instanceID string, record apikey.Record) error {
	if !domain.ValidUUID(instanceID) {
		return fmt.Errorf("invalid API key instance")
	}
	return validateAPIKeyRecord(record)
}

func validateAPIKeyRecord(record apikey.Record) error {
	if !domain.ValidUUID(record.ID) || !domain.ValidClientName(record.Name) || record.CreatedAt.IsZero() {
		return fmt.Errorf("invalid API key record")
	}
	return nil
}
