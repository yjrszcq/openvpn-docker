package migration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	"github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
)

const maxLegacyMetadata = 4 << 20

var legacyKeys = map[string]struct{}{
	"OVPN_CONFIG_VERSION": {}, "OVPN_ENDPOINT": {}, "OVPN_PROTO": {},
	"OVPN_TRANSPORT_FAMILY": {}, "OVPN_PORT": {}, "OVPN_NETWORK": {},
	"OVPN_TOPOLOGY": {}, "OVPN_DYNAMIC_POOL_SIZE": {}, "OVPN_NAT": {},
	"OVPN_NAT_INTERFACE": {}, "OVPN_REDIRECT_GATEWAY": {}, "OVPN_CLIENT_TO_CLIENT": {},
	"OVPN_DNS": {}, "OVPN_ROUTES": {}, "OVPN_LOG_MAX_BYTES": {}, "OVPN_LOG_BACKUPS": {},
}

// ReadSchema3 returns one immutable, verified view of a schema 3 data root.
func ReadSchema3(ctx context.Context, root string, now time.Time) (Source, error) {
	reader, err := newReader(root)
	if err != nil {
		return Source{}, err
	}
	probe, err := reader.probe(ctx)
	if err != nil {
		return Source{}, err
	}
	if probe.Status != SourceSchema3 {
		switch probe.Status {
		case SourceLegacy:
			return Source{}, fmt.Errorf("%w: schema %d must be upgraded to schema 3 before Go migration", ErrNeedsShellUpgrade, probe.FileVersion)
		case SourceNewer:
			return Source{}, fmt.Errorf("%w: source schema %d is newer than supported schema 3", ErrUnsupportedSource, probe.FileVersion)
		default:
			return Source{}, fmt.Errorf("%w: source status is %s", ErrInvalidSource, probe.Status)
		}
	}
	configuration, err := reader.readConfig(ctx)
	if err != nil {
		return Source{}, err
	}
	instance, err := reader.readInstance(ctx)
	if err != nil {
		return Source{}, err
	}
	identities, err := reader.readIdentities(ctx)
	if err != nil {
		return Source{}, err
	}
	clients, canonical, err := reader.readAssignments(ctx, configuration, identities)
	if err != nil {
		return Source{}, err
	}
	audit, err := reader.readAudit(ctx)
	if err != nil {
		return Source{}, err
	}
	source := Source{Root: root, Probe: probe, Config: configuration, Instance: instance, Clients: clients, Audit: audit, CanonicalClientIPs: canonical, Repairs: []Issue{}, Leases: []Lease{}, Artifacts: []Artifact{}}
	if !canonical {
		source.Repairs = append(source.Repairs, Issue{Code: "CLIENT_IP_REGISTRY_NOT_CANONICAL", Path: "meta/client-ip.csv", Detail: "registry will be normalized during migration"})
	}
	if err := reader.verifySecurity(ctx, &source, now); err != nil {
		return Source{}, err
	}
	leases, repairs, err := reader.readLeases(ctx, source.Config, source.Clients)
	if err != nil {
		return Source{}, err
	}
	source.Leases = leases
	source.Repairs = append(source.Repairs, repairs...)
	return source, nil
}

type reader struct{ root string }

func newReader(root string) (*reader, error) {
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, invalid("", "data directory must be a clean absolute path")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, invalid("", "inspect data directory: %v", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, invalid("", "data directory is not a safe private directory")
	}
	return &reader{root: root}, nil
}

func (r *reader) probe(ctx context.Context) (Probe, error) {
	fileData, fileErr := r.read(ctx, "config/schema-version", 0o600, 64)
	projectData, projectErr := r.read(ctx, "config/project.env", 0o600, maxLegacyMetadata)
	if fileErr != nil || projectErr != nil {
		if errors.Is(fileErr, os.ErrNotExist) && errors.Is(projectErr, os.ErrNotExist) {
			return Probe{Status: SourceUnknown}, nil
		}
		return Probe{Status: SourceUnknown}, invalid("config", "schema markers are missing, unsafe, or unreadable")
	}
	fileVersion, err := parseVersionFile(fileData)
	if err != nil {
		return Probe{Status: SourceUnknown}, invalid("config/schema-version", "%v", err)
	}
	values, err := parseProjectEnv(projectData, false)
	if err != nil {
		return Probe{Status: SourceUnknown, FileVersion: fileVersion}, err
	}
	projectVersion, err := strconv.Atoi(values["OVPN_CONFIG_VERSION"])
	if err != nil {
		return Probe{Status: SourceUnknown, FileVersion: fileVersion}, invalid("config/project.env", "OVPN_CONFIG_VERSION is not an integer")
	}
	probe := Probe{ProjectVersion: projectVersion, FileVersion: fileVersion}
	if projectVersion != fileVersion {
		probe.Status = SourceConflict
		return probe, nil
	}
	switch fileVersion {
	case 1, 2:
		probe.Status = SourceLegacy
	case LegacySchema:
		probe.Status = SourceSchema3
	default:
		if fileVersion > LegacySchema {
			probe.Status = SourceNewer
		} else {
			probe.Status = SourceUnknown
		}
	}
	return probe, nil
}

func (r *reader) readConfig(ctx context.Context) (domain.Config, error) {
	data, err := r.read(ctx, "config/project.env", 0o600, maxLegacyMetadata)
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "%v", err)
	}
	values, err := parseProjectEnv(data, true)
	if err != nil {
		return domain.Config{}, err
	}
	if values["OVPN_CONFIG_VERSION"] != "3" || values["OVPN_TOPOLOGY"] != "subnet" {
		return domain.Config{}, invalid("config/project.env", "expected schema 3 with subnet topology")
	}
	port, err := parseUnsigned(values, "OVPN_PORT", 16)
	if err != nil || port == 0 {
		return domain.Config{}, invalid("config/project.env", "OVPN_PORT is invalid")
	}
	pool, err := parseUnsigned(values, "OVPN_DYNAMIC_POOL_SIZE", 64)
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "OVPN_DYNAMIC_POOL_SIZE is invalid")
	}
	maxBytes, err := parseUnsigned(values, "OVPN_LOG_MAX_BYTES", 64)
	if err != nil || maxBytes == 0 {
		return domain.Config{}, invalid("config/project.env", "OVPN_LOG_MAX_BYTES is invalid")
	}
	backups, err := parseUnsigned(values, "OVPN_LOG_BACKUPS", 32)
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "OVPN_LOG_BACKUPS is invalid")
	}
	nat, err := parseBool(values["OVPN_NAT"])
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "OVPN_NAT is invalid")
	}
	redirect, err := parseBool(values["OVPN_REDIRECT_GATEWAY"])
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "OVPN_REDIRECT_GATEWAY is invalid")
	}
	clientToClient, err := parseBool(values["OVPN_CLIENT_TO_CLIENT"])
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "OVPN_CLIENT_TO_CLIENT is invalid")
	}
	payload := map[string]any{
		"version": 1,
		"server":  map[string]any{"endpoint": values["OVPN_ENDPOINT"], "transport": map[string]any{"protocol": values["OVPN_PROTO"], "family": values["OVPN_TRANSPORT_FAMILY"], "port": port}, "clientToClient": clientToClient},
		"ipv4":    map[string]any{"network": values["OVPN_NETWORK"], "dynamicPoolSize": pool, "nat": map[string]any{"enabled": nat, "interface": values["OVPN_NAT_INTERFACE"]}, "redirectGateway": redirect, "dns": splitList(values["OVPN_DNS"]), "routes": splitList(values["OVPN_ROUTES"])},
		"logging": map[string]any{"maxBytes": maxBytes, "backups": backups},
	}
	encoded, _ := json.Marshal(payload)
	value, err := config.Parse(encoded)
	if err != nil {
		return domain.Config{}, invalid("config/project.env", "%v", err)
	}
	return value, nil
}

func (r *reader) readInstance(ctx context.Context) (Instance, error) {
	data, err := r.read(ctx, "meta/instance.json", 0o600, maxLegacyMetadata)
	if err != nil {
		return Instance{}, invalid("meta/instance.json", "%v", err)
	}
	if err := rejectDuplicateJSON(data); err != nil {
		return Instance{}, invalid("meta/instance.json", "%v", err)
	}
	var raw struct {
		SchemaVersion int    `json:"schema_version"`
		InstanceID    string `json:"instance_id"`
		InitializedAt string `json:"initialized_at"`
		ServerName    string `json:"server_name"`
		DataDir       string `json:"data_dir"`
		CAFingerprint string `json:"ca_fingerprint_sha256"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Instance{}, invalid("meta/instance.json", "%v", err)
	}
	if raw.SchemaVersion != 1 || !domain.ValidUUID(raw.InstanceID) {
		return Instance{}, invalid("meta/instance.json", "metadata schema or instance UUID is invalid")
	}
	if raw.ServerName != initialize.DefaultServerName {
		return Instance{}, invalid("meta/instance.json", "unsupported server name %q", raw.ServerName)
	}
	if !filepath.IsAbs(raw.DataDir) || filepath.Clean(raw.DataDir) != raw.DataDir {
		return Instance{}, invalid("meta/instance.json", "data_dir is invalid")
	}
	created, err := time.Parse(time.RFC3339, raw.InitializedAt)
	if err != nil || raw.InitializedAt != created.UTC().Format(time.RFC3339) {
		return Instance{}, invalid("meta/instance.json", "initialized_at is not canonical RFC3339 UTC")
	}
	fingerprint, err := parseFingerprint(raw.CAFingerprint)
	if err != nil {
		return Instance{}, invalid("meta/instance.json", "%v", err)
	}
	idData, err := r.read(ctx, "meta/instance-id", 0o600, 128)
	if err != nil || string(idData) != raw.InstanceID+"\n" {
		return Instance{}, invalid("meta/instance-id", "does not match metadata instance UUID")
	}
	return Instance{ID: raw.InstanceID, InitializedAt: created, ServerName: raw.ServerName, DataDir: raw.DataDir, CAFingerprint: fingerprint}, nil
}

func (r *reader) readIdentities(ctx context.Context) ([]domain.Client, error) {
	data, err := r.read(ctx, "meta/client-state.csv", 0o600, maxLegacyMetadata)
	if err != nil {
		return nil, invalid("meta/client-state.csv", "%v", err)
	}
	lines, err := strictLines(data)
	if err != nil || len(lines) == 0 || lines[0] != "# id,name,state" {
		return nil, invalid("meta/client-state.csv", "invalid header or line endings")
	}
	values := make([]domain.Client, 0, len(lines)-1)
	ids := map[string]bool{}
	names := map[string]bool{}
	for index, line := range lines[1:] {
		if strings.ContainsAny(line, " \t") {
			return nil, invalid("meta/client-state.csv", "line %d contains whitespace", index+2)
		}
		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return nil, invalid("meta/client-state.csv", "line %d must have three fields", index+2)
		}
		client, parseErr := domain.NewClient(parts[0], parts[1], domain.ClientStatus(parts[2]))
		if parseErr != nil || ids[parts[0]] {
			return nil, invalid("meta/client-state.csv", "line %d is invalid or duplicates a UUID", index+2)
		}
		if client.Status != domain.ClientDeleted && names[client.Name] {
			return nil, invalid("meta/client-state.csv", "line %d duplicates a current name", index+2)
		}
		ids[client.ID] = true
		if client.Status != domain.ClientDeleted {
			names[client.Name] = true
		}
		values = append(values, client)
	}
	return values, nil
}

func (r *reader) readAssignments(ctx context.Context, configuration domain.Config, identities []domain.Client) ([]Client, bool, error) {
	data, err := r.read(ctx, "meta/client-ip.csv", 0o600, maxLegacyMetadata)
	if err != nil {
		return nil, false, invalid("meta/client-ip.csv", "%v", err)
	}
	lines, err := strictLines(data)
	if err != nil || len(lines) == 0 || lines[0] != "# id,name,ip" {
		return nil, false, invalid("meta/client-ip.csv", "invalid header or line endings")
	}
	byID := make(map[string]domain.Client, len(identities))
	resultByID := make(map[string]Client, len(identities))
	for _, identity := range identities {
		byID[identity.ID] = identity
		resultByID[identity.ID] = Client{Client: identity}
	}
	layout, _ := ipam.NewIPv4Layout(configuration.IPv4.Network, configuration.IPv4.DynamicPoolSize)
	seenNames := map[string]bool{}
	seenAddresses := map[string]bool{}
	for index, line := range lines[1:] {
		if strings.ContainsAny(line, " \t") {
			return nil, false, invalid("meta/client-ip.csv", "line %d contains whitespace", index+2)
		}
		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return nil, false, invalid("meta/client-ip.csv", "line %d must have three fields", index+2)
		}
		identity, ok := byID[parts[0]]
		if !ok || identity.Status == domain.ClientDeleted || identity.Name != parts[1] || seenNames[parts[1]] {
			return nil, false, invalid("meta/client-ip.csv", "line %d does not match one current identity", index+2)
		}
		value := resultByID[identity.ID]
		if parts[2] != "" {
			address, parseErr := domain.ParseAddress(parts[2])
			if parseErr != nil || layout.ValidateStatic(address) != nil || seenAddresses[address.String()] {
				return nil, false, invalid("meta/client-ip.csv", "line %d has an invalid or duplicate static address", index+2)
			}
			value.Address = &address
			seenAddresses[address.String()] = true
		}
		resultByID[identity.ID] = value
		seenNames[parts[1]] = true
	}
	for _, identity := range identities {
		if identity.Status != domain.ClientDeleted && !seenNames[identity.Name] {
			return nil, false, invalid("meta/client-ip.csv", "current client %q is missing", identity.Name)
		}
	}
	result := make([]Client, 0, len(identities))
	for _, identity := range identities {
		result = append(result, resultByID[identity.ID])
	}
	return result, bytes.Equal(data, canonicalAssignments(result)), nil
}

func (r *reader) readAudit(ctx context.Context) ([]AuditEvent, error) {
	data, err := r.read(ctx, "meta/audit.jsonl", 0o600, maxLegacyMetadata)
	if err != nil {
		return nil, invalid("meta/audit.jsonl", "%v", err)
	}
	if len(data) == 0 {
		return []AuditEvent{}, nil
	}
	lines, err := strictLines(data)
	if err != nil {
		return nil, invalid("meta/audit.jsonl", "%v", err)
	}
	values := make([]AuditEvent, 0, len(lines))
	for index, line := range lines {
		if err := rejectDuplicateJSON([]byte(line)); err != nil {
			return nil, invalid("meta/audit.jsonl", "line %d: %v", index+1, err)
		}
		var value AuditEvent
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&value); err != nil || !validAudit(value) {
			return nil, invalid("meta/audit.jsonl", "line %d has an unsupported event shape", index+1)
		}
		value.Raw = []byte(line)
		values = append(values, value)
	}
	return values, nil
}

func (r *reader) verifySecurity(ctx context.Context, source *Source, now time.Time) error {
	pkiDir := filepath.Join(r.root, "pki")
	ca, err := pki.ValidateCAKeyPair(pkiDir, now)
	if err != nil {
		return invalid("pki", "%v", err)
	}
	if ca.Fingerprint != source.Instance.CAFingerprint {
		return invalid("meta/instance.json", "CA fingerprint does not match pki/ca.crt")
	}
	server, err := pki.ValidateServer(pkiDir, source.Instance.ServerName, now)
	if err != nil {
		return invalid("pki", "%v", err)
	}
	if err := pki.ValidateCRLForCA(pkiDir, now); err != nil {
		return invalid("pki/crl.pem", "%v", err)
	}
	if err := pki.ValidateTLSCryptKey(filepath.Join(r.root, "secrets", "tls-crypt.key")); err != nil {
		return invalid("secrets/tls-crypt.key", "%v", err)
	}
	pkiStates, err := r.readPKIIndex(ctx, source.Instance.ServerName)
	if err != nil {
		return err
	}
	for index := range source.Clients {
		client := &source.Clients[index]
		state, exists := pkiStates[client.Client.ID]
		if client.Client.Status == domain.ClientDeleted {
			if exists && state == domain.ClientActive {
				return invalid("pki/index.txt", "deleted client %s has an active certificate", client.Client.ID)
			}
			continue
		}
		if !exists || state != client.Client.Status {
			return invalid("pki/index.txt", "certificate state disagrees for client %s", client.Client.ID)
		}
		info, validateErr := pki.ValidateClient(pkiDir, client.Client.ID, now)
		if validateErr != nil {
			return invalid("pki", "client %s: %v", client.Client.ID, validateErr)
		}
		if err := pki.ValidateRevocationStatus(pkiDir, info.Serial, client.Client.Status == domain.ClientRevoked, now); err != nil {
			return invalid("pki/crl.pem", "client %s: %v", client.Client.ID, err)
		}
		client.Certificate = &info
		profileDir := "active"
		if client.Client.Status == domain.ClientRevoked {
			profileDir = "revoked"
		}
		client.ProfileKey = "clients/" + profileDir + "/" + client.Client.Name + ".ovpn"
		if err := r.verifyProfile(ctx, *client, now); err != nil {
			return err
		}
	}
	for id := range pkiStates {
		found := false
		for _, client := range source.Clients {
			if client.Client.ID == id {
				found = true
				break
			}
		}
		if !found {
			return invalid("pki/index.txt", "client certificate %s is absent from identity registry", id)
		}
	}
	instanceArtifacts := []struct {
		kind, key string
		cert      *pki.CertificateInfo
	}{
		{"ca-cert", "pki/ca.crt", &ca}, {"ca-key", "pki/private/ca.key", nil}, {"server-cert", "pki/issued/" + source.Instance.ServerName + ".crt", &server}, {"server-key", "pki/private/" + source.Instance.ServerName + ".key", nil}, {"crl", "pki/crl.pem", nil}, {"tls-crypt", "secrets/tls-crypt.key", nil},
	}
	for _, item := range instanceArtifacts {
		value, ref, readErr := r.readArtifact(ctx, item.key)
		if readErr != nil {
			return invalid(item.key, "%v", readErr)
		}
		source.Artifacts = append(source.Artifacts, artifactValue("instance", source.Instance.ID, item.kind, ref, value, item.cert))
	}
	for _, client := range source.Clients {
		if client.Client.Status == domain.ClientDeleted {
			continue
		}
		for _, item := range []struct {
			kind, key string
			cert      *pki.CertificateInfo
		}{{"client-cert", "pki/issued/" + client.Client.ID + ".crt", client.Certificate}, {"client-key", "pki/private/" + client.Client.ID + ".key", nil}, {"profile", client.ProfileKey, nil}} {
			value, ref, readErr := r.readArtifact(ctx, item.key)
			if readErr != nil {
				return invalid(item.key, "%v", readErr)
			}
			source.Artifacts = append(source.Artifacts, artifactValue("client", client.Client.ID, item.kind, ref, value, item.cert))
		}
	}
	return nil
}

func (r *reader) verifyProfile(ctx context.Context, client Client, now time.Time) error {
	profile, _, err := r.readArtifact(ctx, client.ProfileKey)
	if err != nil {
		return invalid(client.ProfileKey, "%v", err)
	}
	blocks, err := parseProfile(profile, client.Client.ID, client.Client.Name)
	if err != nil {
		return invalid(client.ProfileKey, "%v", err)
	}
	if _, err := pki.ValidateClientMaterial(blocks["ca"], blocks["cert"], blocks["key"], client.Client.ID, now); err != nil {
		return invalid(client.ProfileKey, "%v", err)
	}
	if err := pki.ValidateTLSCryptData(blocks["tls-crypt"]); err != nil {
		return invalid(client.ProfileKey, "%v", err)
	}
	for block, key := range map[string]string{"ca": "pki/ca.crt", "cert": "pki/issued/" + client.Client.ID + ".crt", "key": "pki/private/" + client.Client.ID + ".key", "tls-crypt": "secrets/tls-crypt.key"} {
		persisted, _, readErr := r.readArtifact(ctx, key)
		if readErr != nil || digest(persisted) != digest(blocks[block]) {
			return invalid(client.ProfileKey, "%s block does not match %s", block, key)
		}
	}
	return nil
}

func (r *reader) readPKIIndex(ctx context.Context, serverName string) (map[string]domain.ClientStatus, error) {
	data, err := r.read(ctx, "pki/index.txt", 0o600, maxLegacyMetadata)
	if err != nil {
		return nil, invalid("pki/index.txt", "%v", err)
	}
	lines, err := strictLines(data)
	if err != nil && len(data) != 0 {
		return nil, invalid("pki/index.txt", "%v", err)
	}
	states := map[string]domain.ClientStatus{}
	for index, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) < 6 {
			return nil, invalid("pki/index.txt", "line %d is malformed", index+1)
		}
		if fields[0] != "V" && fields[0] != "R" {
			continue
		}
		id := extractCN(fields[len(fields)-1])
		if id == serverName {
			continue
		}
		if !domain.ValidUUID(id) {
			if fields[0] == "R" {
				continue
			}
			return nil, invalid("pki/index.txt", "line %d has invalid active client CN", index+1)
		}
		if fields[0] == "V" {
			states[id] = domain.ClientActive
		} else if _, ok := states[id]; !ok {
			states[id] = domain.ClientRevoked
		}
	}
	return states, nil
}

func (r *reader) readLeases(ctx context.Context, configuration domain.Config, clients []Client) ([]Lease, []Issue, error) {
	directory := filepath.Join(r.root, "cache", "client-leases")
	info, err := os.Lstat(directory)
	if errors.Is(err, os.ErrNotExist) {
		return []Lease{}, []Issue{}, nil
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o022 != 0 {
		return nil, nil, invalid("cache/client-leases", "directory is unsafe")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, nil, invalid("cache/client-leases", "%v", err)
	}
	byID := map[string]Client{}
	for _, client := range clients {
		byID[client.Client.ID] = client
	}
	layout, _ := ipam.NewIPv4Layout(configuration.IPv4.Network, configuration.IPv4.DynamicPoolSize)
	seen := map[string]bool{}
	values := make([]Lease, 0, len(entries))
	issues := []Issue{}
	for _, entry := range entries {
		if !domain.ValidUUID(entry.Name()) {
			return nil, nil, invalid("cache/client-leases", "invalid lease filename %q", entry.Name())
		}
		data, readErr := r.read(ctx, "cache/client-leases/"+entry.Name(), 0o600, 128)
		if readErr != nil {
			return nil, nil, invalid("cache/client-leases/"+entry.Name(), "%v", readErr)
		}
		address, parseErr := domain.ParseAddress(strings.TrimSuffix(string(data), "\n"))
		stat, _ := entry.Info()
		lease := Lease{ClientID: entry.Name(), Address: address, UpdatedAt: stat.ModTime().UTC()}
		client, found := byID[entry.Name()]
		switch {
		case parseErr != nil || !bytes.HasSuffix(data, []byte("\n")) || bytes.Count(data, []byte("\n")) != 1:
			lease.Reason = "invalid lease content"
		case !found || client.Client.Status == domain.ClientDeleted:
			lease.Reason = "lease owner is not a current client"
		case client.Address != nil:
			lease.Reason = "static clients do not retain dynamic leases"
		case layout.ValidateDynamicLease(address) != nil:
			lease.Reason = "address is outside the dynamic pool"
		case seen[address.String()]:
			lease.Reason = "address duplicates another lease"
		default:
			lease.Import = true
			seen[address.String()] = true
		}
		if !lease.Import {
			issues = append(issues, Issue{Code: "LEASE_DISCARDED", Path: "cache/client-leases/" + entry.Name(), Detail: lease.Reason})
		}
		values = append(values, lease)
	}
	sort.Slice(values, func(i, j int) bool { return values[i].ClientID < values[j].ClientID })
	return values, issues, nil
}

func (r *reader) read(ctx context.Context, key string, mode os.FileMode, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := artifact.ValidateKey(key); err != nil {
		return nil, err
	}
	current := r.root
	parts := strings.Split(filepath.FromSlash(key), string(os.PathSeparator))
	for index, part := range parts {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("symlink traversal is forbidden")
		}
		if index < len(parts)-1 {
			if !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
				return nil, fmt.Errorf("unsafe parent directory")
			}
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != mode {
			return nil, fmt.Errorf("file mode is %04o, want %04o", info.Mode().Perm(), mode)
		}
	}
	file, err := os.Open(current)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds size limit")
	}
	return data, ctx.Err()
}

func (r *reader) readArtifact(ctx context.Context, key string) ([]byte, artifact.Reference, error) {
	mode := os.FileMode(0o644)
	if strings.Contains(key, "/private/") || strings.HasPrefix(key, "secrets/") || strings.HasPrefix(key, "clients/") {
		mode = 0o600
	}
	data, err := r.read(ctx, key, mode, artifact.MaxArtifactSize)
	if err != nil {
		return nil, artifact.Reference{}, err
	}
	return data, artifact.Reference{Backend: artifact.BackendLocal, Key: key, Digest: digest(data), Mode: uint32(mode)}, nil
}

func parseVersionFile(data []byte) (int, error) {
	if !bytes.HasSuffix(data, []byte("\n")) || bytes.Count(data, []byte("\n")) != 1 {
		return 0, fmt.Errorf("must contain one integer line")
	}
	value, err := strconv.Atoi(strings.TrimSuffix(string(data), "\n"))
	if err != nil || value < 1 {
		return 0, fmt.Errorf("must contain a positive integer")
	}
	return value, nil
}

func parseProjectEnv(data []byte, requireAll bool) (map[string]string, error) {
	lines, err := strictLines(data)
	if err != nil {
		return nil, invalid("config/project.env", "%v", err)
	}
	values := map[string]string{}
	for index, line := range lines {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, invalid("config/project.env", "line %d has no equals sign", index+1)
		}
		if _, allowed := legacyKeys[key]; !allowed {
			return nil, invalid("config/project.env", "line %d has unsupported key %q", index+1, key)
		}
		if _, duplicate := values[key]; duplicate {
			return nil, invalid("config/project.env", "line %d duplicates key %q", index+1, key)
		}
		values[key] = value
	}
	if _, ok := values["OVPN_CONFIG_VERSION"]; !ok {
		return nil, invalid("config/project.env", "OVPN_CONFIG_VERSION is missing")
	}
	if requireAll {
		for key := range legacyKeys {
			if _, ok := values[key]; !ok {
				return nil, invalid("config/project.env", "required key %s is missing", key)
			}
		}
	}
	return values, nil
}

func strictLines(data []byte) ([]string, error) {
	if bytes.Contains(data, []byte("\r")) {
		return nil, fmt.Errorf("CR line endings are forbidden")
	}
	if len(data) == 0 {
		return []string{}, nil
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return nil, fmt.Errorf("final newline is required")
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"), nil
}

func parseUnsigned(values map[string]string, key string, bits int) (uint64, error) {
	return strconv.ParseUint(values[key], 10, bits)
}
func parseBool(value string) (bool, error) {
	if value == "true" {
		return true, nil
	}
	if value == "false" {
		return false, nil
	}
	return false, fmt.Errorf("not a bool")
}
func splitList(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}

func parseFingerprint(value string) ([32]byte, error) {
	var result [32]byte
	compact := strings.ReplaceAll(value, ":", "")
	decoded, err := hex.DecodeString(compact)
	if err != nil || len(decoded) != len(result) {
		return result, fmt.Errorf("CA fingerprint must be SHA-256")
	}
	copy(result[:], decoded)
	return result, nil
}

func canonicalAssignments(clients []Client) []byte {
	static, dynamic := make([]Client, 0), make([]Client, 0)
	for _, client := range clients {
		if client.Client.Status == domain.ClientDeleted {
			continue
		}
		if client.Address == nil {
			dynamic = append(dynamic, client)
		} else {
			static = append(static, client)
		}
	}
	sort.Slice(static, func(i, j int) bool {
		if static[i].Address.String() != static[j].Address.String() {
			return static[i].Address.Netip().Less(static[j].Address.Netip())
		}
		if static[i].Client.Name != static[j].Client.Name {
			return static[i].Client.Name < static[j].Client.Name
		}
		return static[i].Client.ID < static[j].Client.ID
	})
	sort.Slice(dynamic, func(i, j int) bool {
		if dynamic[i].Client.Name != dynamic[j].Client.Name {
			return dynamic[i].Client.Name < dynamic[j].Client.Name
		}
		return dynamic[i].Client.ID < dynamic[j].Client.ID
	})
	var output strings.Builder
	output.WriteString("# id,name,ip\n")
	for _, client := range append(static, dynamic...) {
		address := ""
		if client.Address != nil {
			address = client.Address.String()
		}
		fmt.Fprintf(&output, "%s,%s,%s\n", client.Client.ID, client.Client.Name, address)
	}
	return []byte(output.String())
}

func validAudit(value AuditEvent) bool {
	canonical := value.Timestamp.UTC().Format(time.RFC3339)
	if value.Timestamp.IsZero() || value.Timestamp.Location() != time.UTC || canonical != value.Timestamp.Format(time.RFC3339) {
		return false
	}
	nullClients := value.ClientID == nil && value.ClientName == nil
	if value.Legacy {
		return value.SourceSchema == 2 && nullClients && value.OldName == "" && ((value.Event == "client_ip_apply" || value.Event == "network_migration") && (value.Outcome == "applied" || value.Outcome == "rejected") && value.Operation == "" || value.Event == "client_lifecycle" && (value.Operation == "revoke" || value.Operation == "reissue" || value.Operation == "delete" || value.Operation == "release_ip") && (value.Outcome == "applied" || value.Outcome == "rejected" || value.Outcome == "failed"))
	}
	if value.SourceSchema != 0 || value.ClientID == nil != (value.ClientName == nil) {
		return false
	}
	if value.Event == "client_ip_apply" || value.Event == "network_migration" {
		return nullClients && value.Operation == "" && value.OldName == "" && (value.Outcome == "applied" || value.Outcome == "rejected")
	}
	if value.ClientID == nil || !domain.ValidUUID(*value.ClientID) || !domain.ValidClientName(*value.ClientName) {
		return false
	}
	if value.Event == "client_rename" {
		return value.Operation == "" && value.Outcome == "applied" && domain.ValidClientName(value.OldName)
	}
	return value.Event == "client_lifecycle" && value.OldName == "" && (value.Operation == "revoke" || value.Operation == "reissue" || value.Operation == "delete" || value.Operation == "release_ip") && (value.Outcome == "applied" || value.Outcome == "rejected" || value.Outcome == "failed")
}

func rejectDuplicateJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var walk func() error
	walk = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("object key is not a string")
				}
				if seen[key] {
					return fmt.Errorf("duplicate key %q", key)
				}
				seen[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim('}') {
				return fmt.Errorf("invalid object")
			}
		case '[':
			for decoder.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			end, err := decoder.Token()
			if err != nil || end != json.Delim(']') {
				return fmt.Errorf("invalid array")
			}
		default:
			return fmt.Errorf("unexpected delimiter")
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func parseProfile(data []byte, id, name string) (map[string][]byte, error) {
	text := string(data)
	if !exactComment(text, "# ovpn-client-id: ", id) || !exactComment(text, "# ovpn-client-name: ", name) {
		return nil, fmt.Errorf("identity comments do not match registry")
	}
	values := map[string][]byte{}
	for _, block := range []string{"ca", "cert", "key", "tls-crypt"} {
		open, close := "<"+block+">", "</"+block+">"
		if strings.Count(text, open) != 1 || strings.Count(text, close) != 1 {
			return nil, fmt.Errorf("profile must contain one %s block", block)
		}
		start := strings.Index(text, open) + len(open)
		end := strings.Index(text[start:], close)
		if end < 0 {
			return nil, fmt.Errorf("profile %s block is not closed", block)
		}
		value := strings.TrimSpace(text[start : start+end])
		if value == "" {
			return nil, fmt.Errorf("profile %s block is empty", block)
		}
		values[block] = []byte(value + "\n")
	}
	return values, nil
}

func exactComment(text, prefix, wanted string) bool {
	count := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			count++
			if strings.TrimSpace(strings.TrimPrefix(line, prefix)) != wanted {
				return false
			}
		}
	}
	return count == 1
}
func extractCN(subject string) string {
	marker := "/CN="
	index := strings.LastIndex(subject, marker)
	if index < 0 {
		return ""
	}
	value := subject[index+len(marker):]
	if end := strings.IndexByte(value, '/'); end >= 0 {
		value = value[:end]
	}
	return value
}

func artifactValue(ownerKind, ownerID, kind string, ref artifact.Reference, data []byte, certificate *pki.CertificateInfo) Artifact {
	value := Artifact{OwnerKind: ownerKind, OwnerID: ownerID, Kind: kind, Key: ref.Key, Digest: sha256.Sum256(data), Mode: ref.Mode}
	if certificate != nil {
		value.CertificateSerial = certificate.Serial
		value.CertificateFingerprint = append([]byte(nil), certificate.Fingerprint[:]...)
	}
	return value
}
