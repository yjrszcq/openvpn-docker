package sqlite

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"time"

	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
)

// InstanceState is the authoritative instance and current applied config view.
type InstanceState struct {
	ID            string
	CreatedAt     time.Time
	CAFingerprint [32]byte
	NetworkID     string
	Applied       configservice.AppliedSnapshot
}

// LoadOnlyInstance returns the sole instance owned by this data directory.
func (store *Store) LoadOnlyInstance(ctx context.Context) (InstanceState, error) {
	rows, err := store.db.QueryContext(ctx, "SELECT id FROM instances ORDER BY id")
	if err != nil {
		return InstanceState{}, fmt.Errorf("list instances: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return InstanceState{}, err
		}
		ids = append(ids, id)
	}
	rowErr := rows.Err()
	closeErr := rows.Close()
	if rowErr != nil || closeErr != nil {
		return InstanceState{}, errors.Join(rowErr, closeErr)
	}
	if len(ids) != 1 || !domain.ValidUUID(ids[0]) {
		return InstanceState{}, fmt.Errorf("%w: expected exactly one instance, found %d", ErrSchema, len(ids))
	}
	return store.LoadInstance(ctx, ids[0])
}

// CreateInstance inserts one instance and its first applied configuration.
func (store *Store) CreateInstance(ctx context.Context, state InstanceState) error {
	if !domain.ValidUUID(state.ID) || state.CreatedAt.IsZero() {
		return fmt.Errorf("invalid instance identity")
	}
	if err := validateApplied(state.Applied); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin instance creation", err)
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO instances(id, created_at, ca_fingerprint, current_applied_revision)
VALUES(?, ?, ?, NULL)`, state.ID, state.CreatedAt.UTC().Truncate(time.Second).Format(time.RFC3339), state.CAFingerprint[:]); err != nil {
		return classifySQLite("insert instance", err)
	}
	if err := writeApplied(ctx, transaction, state.ID, state.Applied); err != nil {
		return err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, state.ID, operationID, "instance.created", map[string]any{"revision": state.Applied.Revision}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return classifySQLite("commit instance creation", err)
	}
	return nil
}

// ApplyConfig atomically advances an existing instance by exactly one revision.
func (store *Store) ApplyConfig(ctx context.Context, instanceID string, snapshot configservice.AppliedSnapshot) error {
	if !domain.ValidUUID(instanceID) {
		return fmt.Errorf("invalid instance UUID")
	}
	if err := validateApplied(snapshot); err != nil {
		return err
	}
	transaction, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		return classifySQLite("begin applied config update", err)
	}
	defer transaction.Rollback()
	var current uint64
	if err := transaction.QueryRowContext(ctx, "SELECT current_applied_revision FROM instances WHERE id = ?", instanceID).Scan(&current); err != nil {
		return fmt.Errorf("%w: read current applied revision: %v", ErrSchema, err)
	}
	if current == math.MaxUint64 || uint64(snapshot.Revision) != current+1 {
		return fmt.Errorf("applied revision must advance from %d to %d", current, current+1)
	}
	var currentNetworkID string
	if err := transaction.QueryRowContext(ctx, "SELECT id FROM networks WHERE instance_id = ? AND family = 4 AND purpose = 'tunnel' AND enabled = 1", instanceID).Scan(&currentNetworkID); err != nil {
		return fmt.Errorf("load current tunnel network: %w", err)
	}
	currentLayout, err := loadNetworkLayout(ctx, transaction, currentNetworkID)
	if err != nil {
		return err
	}
	networkChanged := currentLayout.Network != snapshot.Config.IPv4.Network || currentLayout.Dynamic.Capacity != snapshot.Config.IPv4.DynamicPoolSize
	if networkChanged {
		var assignments int
		if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM address_assignments a JOIN clients c ON c.id = a.client_id WHERE c.instance_id = ? AND a.status IN ('active', 'retained')`, instanceID).Scan(&assignments); err != nil {
			return classifySQLite("count current assignments", err)
		}
		if assignments != 0 {
			return fmt.Errorf("direct applied config update cannot replace a network with current assignments")
		}
	}
	if err := insertAppliedConfig(ctx, transaction, instanceID, snapshot); err != nil {
		return err
	}
	if err := replaceRoutesAndDNS(ctx, transaction, instanceID, snapshot.Config); err != nil {
		return err
	}
	if networkChanged {
		if _, err := replaceNetwork(ctx, transaction, instanceID, "", snapshot.Config); err != nil {
			return err
		}
	}
	if err := advanceAppliedRevision(ctx, transaction, instanceID, snapshot.Revision); err != nil {
		return err
	}
	operationID, err := domain.GenerateUUID()
	if err != nil {
		return err
	}
	if err := appendAudit(ctx, transaction, instanceID, operationID, "config.applied", map[string]any{"revision": snapshot.Revision, "digest": snapshot.Digest}); err != nil {
		return err
	}
	if err := transaction.Commit(); err != nil {
		return classifySQLite("commit applied config update", err)
	}
	return nil
}

// LoadInstance returns one instance and validates its normalized network state.
func (store *Store) LoadInstance(ctx context.Context, instanceID string) (InstanceState, error) {
	if !domain.ValidUUID(instanceID) {
		return InstanceState{}, fmt.Errorf("invalid instance UUID")
	}
	var state InstanceState
	var createdAt string
	var fingerprint []byte
	var revision uint64
	var digest []byte
	var protocol, family string
	var clientToClient, natEnabled, redirectGateway int
	var port uint16
	var logBackups uint32
	var configValue domain.Config
	err := store.db.QueryRowContext(ctx, `
SELECT i.id, i.created_at, i.ca_fingerprint, c.revision, c.digest,
       c.endpoint, c.protocol, c.transport_family, c.port, c.client_to_client,
       c.nat_enabled, c.nat_interface, c.redirect_gateway, c.log_max_bytes, c.log_backups
FROM instances i
JOIN applied_config c ON c.instance_id = i.id AND c.revision = i.current_applied_revision
WHERE i.id = ?`, instanceID).Scan(
		&state.ID, &createdAt, &fingerprint, &revision, &digest,
		&configValue.Endpoint, &protocol, &family, &port, &clientToClient,
		&natEnabled, &configValue.IPv4.NATInterface, &redirectGateway,
		&configValue.Logging.MaxBytes, &logBackups,
	)
	if err != nil {
		return InstanceState{}, fmt.Errorf("load instance: %w", err)
	}
	parsedTime, err := time.Parse(time.RFC3339, createdAt)
	if err != nil || len(fingerprint) != len(state.CAFingerprint) || len(digest) != 32 {
		return InstanceState{}, fmt.Errorf("%w: invalid instance metadata", ErrSchema)
	}
	state.CreatedAt = parsedTime
	copy(state.CAFingerprint[:], fingerprint)
	configValue.Protocol = domain.Protocol(protocol)
	configValue.TransportFamily = domain.TransportFamily(family)
	configValue.Port = port
	configValue.ClientToClient = clientToClient == 1
	configValue.IPv4.NATEnabled = natEnabled == 1
	configValue.IPv4.RedirectGateway = redirectGateway == 1
	configValue.Logging.Backups = logBackups

	var networkPacked []byte
	var prefix int
	err = store.db.QueryRowContext(ctx, `
SELECT id, network, prefix FROM networks
WHERE instance_id = ? AND family = 4 AND purpose = 'tunnel' AND enabled = 1`, instanceID).Scan(&state.NetworkID, &networkPacked, &prefix)
	if err != nil {
		return InstanceState{}, fmt.Errorf("%w: load tunnel network: %v", ErrSchema, err)
	}
	configValue.IPv4.Network, err = unpackNetwork(networkPacked, prefix)
	if err != nil {
		return InstanceState{}, err
	}
	layout, err := loadPools(ctx, store.db, state.NetworkID, configValue.IPv4.Network)
	if err != nil {
		return InstanceState{}, err
	}
	configValue.IPv4.DynamicPoolSize = layout.Dynamic.Capacity
	configValue.IPv4.Routes, err = loadRoutes(ctx, store.db, instanceID)
	if err != nil {
		return InstanceState{}, err
	}
	configValue.IPv4.DNS, err = loadDNS(ctx, store.db, instanceID)
	if err != nil {
		return InstanceState{}, err
	}
	snapshot, err := configservice.NewAppliedSnapshot(configservice.Revision(revision), configValue)
	if err != nil {
		return InstanceState{}, err
	}
	if snapshot.Digest != hex.EncodeToString(digest) {
		return InstanceState{}, fmt.Errorf("%w: applied configuration digest mismatch", ErrSchema)
	}
	if err := validateApplied(snapshot); err != nil {
		return InstanceState{}, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	state.Applied = snapshot
	return state, nil
}

func validateApplied(snapshot configservice.AppliedSnapshot) error {
	if uint64(snapshot.Revision) > math.MaxInt64 || snapshot.Config.Logging.MaxBytes > math.MaxInt64 {
		return fmt.Errorf("applied configuration exceeds SQLite integer range")
	}
	data, err := configservice.ExportYAML(snapshot)
	if err != nil {
		return fmt.Errorf("invalid applied configuration: %w", err)
	}
	parsed, err := configservice.Parse(data)
	if err != nil {
		return fmt.Errorf("invalid applied configuration: %w", err)
	}
	equal, err := configservice.EqualCanonical(snapshot.Config, parsed)
	if err != nil || !equal {
		return fmt.Errorf("applied configuration is not normalized")
	}
	return nil
}

func writeApplied(ctx context.Context, transaction *sql.Tx, instanceID string, snapshot configservice.AppliedSnapshot) error {
	if err := insertAppliedConfig(ctx, transaction, instanceID, snapshot); err != nil {
		return err
	}
	if err := replaceRoutesAndDNS(ctx, transaction, instanceID, snapshot.Config); err != nil {
		return err
	}
	if _, err := insertNetwork(ctx, transaction, instanceID, "", snapshot.Config); err != nil {
		return err
	}
	return advanceAppliedRevision(ctx, transaction, instanceID, snapshot.Revision)
}

func insertAppliedConfig(ctx context.Context, transaction *sql.Tx, instanceID string, snapshot configservice.AppliedSnapshot) error {
	digest, _ := hex.DecodeString(snapshot.Digest)
	configValue := snapshot.Config
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO applied_config(
    instance_id, revision, digest, endpoint, protocol, transport_family, port,
    client_to_client, nat_enabled, nat_interface, redirect_gateway, log_max_bytes, log_backups
) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		instanceID, snapshot.Revision, digest, configValue.Endpoint, configValue.Protocol,
		configValue.TransportFamily, configValue.Port, boolInt(configValue.ClientToClient),
		boolInt(configValue.IPv4.NATEnabled), configValue.IPv4.NATInterface,
		boolInt(configValue.IPv4.RedirectGateway), configValue.Logging.MaxBytes,
		configValue.Logging.Backups); err != nil {
		return classifySQLite("insert applied configuration", err)
	}
	return nil
}

func replaceRoutesAndDNS(ctx context.Context, transaction *sql.Tx, instanceID string, configValue domain.Config) error {
	for _, statement := range []string{"DELETE FROM pushed_routes WHERE instance_id = ?", "DELETE FROM dns_servers WHERE instance_id = ?"} {
		if _, err := transaction.ExecContext(ctx, statement, instanceID); err != nil {
			return classifySQLite("replace applied network state", err)
		}
	}
	for position, route := range configValue.IPv4.Routes {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO pushed_routes(instance_id, position, family, network, prefix) VALUES(?, ?, 4, ?, ?)`, instanceID, position, packAddress(route.Prefix().Addr()), route.Prefix().Bits()); err != nil {
			return classifySQLite("insert pushed route", err)
		}
	}
	for position, address := range configValue.IPv4.DNS {
		if _, err := transaction.ExecContext(ctx, `INSERT INTO dns_servers(instance_id, position, family, address) VALUES(?, ?, 4, ?)`, instanceID, position, packAddress(address.Netip())); err != nil {
			return classifySQLite("insert DNS server", err)
		}
	}
	return nil
}

func replaceNetwork(ctx context.Context, transaction *sql.Tx, instanceID, networkID string, configValue domain.Config) (string, error) {
	if _, err := transaction.ExecContext(ctx, "UPDATE networks SET enabled = 0 WHERE instance_id = ? AND family = 4 AND purpose = 'tunnel' AND enabled = 1", instanceID); err != nil {
		return "", classifySQLite("disable previous tunnel network", err)
	}
	return insertNetwork(ctx, transaction, instanceID, networkID, configValue)
}

func insertNetwork(ctx context.Context, transaction *sql.Tx, instanceID, networkID string, configValue domain.Config) (string, error) {
	var err error
	if networkID == "" {
		networkID, err = domain.GenerateUUID()
		if err != nil {
			return "", err
		}
	} else if !domain.ValidUUID(networkID) {
		return "", fmt.Errorf("invalid tunnel network UUID")
	}
	if _, err := transaction.ExecContext(ctx, `
INSERT INTO networks(id, instance_id, family, network, prefix, purpose, enabled)
VALUES(?, ?, 4, ?, ?, 'tunnel', 1)`, networkID, instanceID, packAddress(configValue.IPv4.Network.Prefix().Addr()), configValue.IPv4.Network.Prefix().Bits()); err != nil {
		return "", classifySQLite("insert tunnel network", err)
	}
	layout, err := ipam.NewIPv4Layout(configValue.IPv4.Network, configValue.IPv4.DynamicPoolSize)
	if err != nil {
		return "", err
	}
	for _, pool := range []struct {
		kind, policy string
		rangeValue   ipam.AddressRange
	}{{"static", "lowest-free", layout.Static}, {"dynamic", "openvpn-dynamic", layout.Dynamic}} {
		if pool.rangeValue.Empty() {
			continue
		}
		poolID, err := domain.GenerateUUID()
		if err != nil {
			return "", err
		}
		if _, err := transaction.ExecContext(ctx, `
INSERT INTO address_pools(id, network_id, kind, first_address, last_address, policy)
VALUES(?, ?, ?, ?, ?, ?)`, poolID, networkID, pool.kind, packAddress(pool.rangeValue.First.Netip()), packAddress(pool.rangeValue.Last.Netip()), pool.policy); err != nil {
			return "", classifySQLite("insert address pool", err)
		}
	}
	return networkID, nil
}

func advanceAppliedRevision(ctx context.Context, transaction *sql.Tx, instanceID string, revision configservice.Revision) error {
	if _, err := transaction.ExecContext(ctx, "UPDATE instances SET current_applied_revision = ? WHERE id = ?", revision, instanceID); err != nil {
		return classifySQLite("advance applied revision", err)
	}
	return nil
}

func loadPools(ctx context.Context, database *sql.DB, networkID string, network domain.Network) (ipam.IPv4Layout, error) {
	rows, err := database.QueryContext(ctx, "SELECT kind, first_address, last_address, policy FROM address_pools WHERE network_id = ? ORDER BY kind", networkID)
	if err != nil {
		return ipam.IPv4Layout{}, err
	}
	defer rows.Close()
	type storedPool struct {
		first, last domain.Address
		policy      string
	}
	pools := make(map[string]storedPool, 2)
	var dynamic uint64
	for rows.Next() {
		var kind, policy string
		var first, last []byte
		if err := rows.Scan(&kind, &first, &last, &policy); err != nil {
			return ipam.IPv4Layout{}, err
		}
		firstAddress, err := unpackAddress(first)
		if err != nil {
			return ipam.IPv4Layout{}, err
		}
		lastAddress, err := unpackAddress(last)
		if err != nil {
			return ipam.IPv4Layout{}, err
		}
		capacity := uint64(addressUint32(lastAddress)-addressUint32(firstAddress)) + 1
		if kind == "dynamic" {
			dynamic = capacity
		}
		pools[kind] = storedPool{first: firstAddress, last: lastAddress, policy: policy}
	}
	if err := rows.Err(); err != nil {
		return ipam.IPv4Layout{}, err
	}
	layout, err := ipam.NewIPv4Layout(network, dynamic)
	if err != nil {
		return ipam.IPv4Layout{}, fmt.Errorf("%w: invalid address pools", ErrSchema)
	}
	expected := []struct {
		kind, policy string
		value        ipam.AddressRange
	}{{"static", "lowest-free", layout.Static}, {"dynamic", "openvpn-dynamic", layout.Dynamic}}
	expectedCount := 0
	for _, item := range expected {
		if item.value.Empty() {
			continue
		}
		expectedCount++
		stored, exists := pools[item.kind]
		if !exists || stored.first != item.value.First || stored.last != item.value.Last || stored.policy != item.policy {
			return ipam.IPv4Layout{}, fmt.Errorf("%w: %s pool does not match network layout", ErrSchema, item.kind)
		}
	}
	if len(pools) != expectedCount {
		return ipam.IPv4Layout{}, fmt.Errorf("%w: unexpected address pool count", ErrSchema)
	}
	return layout, nil
}

func loadRoutes(ctx context.Context, database *sql.DB, instanceID string) ([]domain.Network, error) {
	rows, err := database.QueryContext(ctx, "SELECT position, network, prefix FROM pushed_routes WHERE instance_id = ? ORDER BY position", instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.Network, 0)
	for rows.Next() {
		var position int
		var packed []byte
		var prefix int
		if err := rows.Scan(&position, &packed, &prefix); err != nil {
			return nil, err
		}
		if position != len(values) {
			return nil, fmt.Errorf("%w: pushed route positions are not contiguous", ErrSchema)
		}
		value, err := unpackNetwork(packed, prefix)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func loadDNS(ctx context.Context, database *sql.DB, instanceID string) ([]domain.Address, error) {
	rows, err := database.QueryContext(ctx, "SELECT position, address FROM dns_servers WHERE instance_id = ? ORDER BY position", instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]domain.Address, 0)
	for rows.Next() {
		var position int
		var packed []byte
		if err := rows.Scan(&position, &packed); err != nil {
			return nil, err
		}
		if position != len(values) {
			return nil, fmt.Errorf("%w: DNS server positions are not contiguous", ErrSchema)
		}
		value, err := unpackAddress(packed)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func packAddress(address netip.Addr) []byte {
	packed := address.As4()
	return append([]byte(nil), packed[:]...)
}

func unpackAddress(packed []byte) (domain.Address, error) {
	if len(packed) != 4 {
		return domain.Address{}, fmt.Errorf("%w: IPv4 address must contain 4 bytes", ErrSchema)
	}
	var value [4]byte
	copy(value[:], packed)
	return domain.AddressFrom4(value), nil
}

func unpackNetwork(packed []byte, prefix int) (domain.Network, error) {
	address, err := unpackAddress(packed)
	if err != nil {
		return domain.Network{}, err
	}
	value, err := domain.NewNetwork(netip.PrefixFrom(address.Netip(), prefix))
	if err != nil {
		return domain.Network{}, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	return value, nil
}

func addressUint32(address domain.Address) uint32 {
	packed := address.Netip().As4()
	return uint32(packed[0])<<24 | uint32(packed[1])<<16 | uint32(packed[2])<<8 | uint32(packed[3])
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
