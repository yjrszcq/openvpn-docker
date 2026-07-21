package migration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	testInstanceID = "11111111-1111-4111-8111-111111111111"
	testClientID   = "22222222-2222-4222-8222-222222222222"
)

type legacyFixture struct {
	root string
	now  time.Time
}

func TestReadSchema3HealthySourceWithoutMutation(t *testing.T) {
	fixture := makeLegacyFixture(t)
	before := snapshotFiles(t, fixture.root)
	source, err := ReadSchema3(context.Background(), fixture.root, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	after := snapshotFiles(t, fixture.root)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("reader mutated source:\nbefore=%v\nafter=%v", before, after)
	}
	if source.Probe.Status != SourceSchema3 || source.Instance.ID != testInstanceID || source.Config.IPv4.Network.String() != "10.42.0.0/24" {
		t.Fatalf("unexpected source: %+v", source)
	}
	if len(source.Clients) != 1 || source.Clients[0].Client.ID != testClientID || source.Clients[0].Certificate == nil || source.Clients[0].ProfileKey != "clients/active/laptop.ovpn" {
		t.Fatalf("unexpected clients: %+v", source.Clients)
	}
	if len(source.Artifacts) != 9 || len(source.Leases) != 1 || !source.Leases[0].Import || !source.CanonicalClientIPs || len(source.Repairs) != 0 {
		t.Fatalf("unexpected migration evidence: artifacts=%d leases=%+v canonical=%v repairs=%+v", len(source.Artifacts), source.Leases, source.CanonicalClientIPs, source.Repairs)
	}
}

func TestProbeSchemaRefusalMatrix(t *testing.T) {
	for _, test := range []struct {
		name, project, file string
		status              SourceStatus
	}{
		{"schema1", "1", "1", SourceLegacy},
		{"schema2", "2", "2", SourceLegacy},
		{"schema3", "3", "3", SourceSchema3},
		{"newer", "4", "4", SourceNewer},
		{"conflict", "3", "2", SourceConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeLegacy(t, root, "config/project.env", "OVPN_CONFIG_VERSION="+test.project+"\n", 0o600)
			writeLegacy(t, root, "config/schema-version", test.file+"\n", 0o600)
			reader, err := newReader(root)
			if err != nil {
				t.Fatal(err)
			}
			probe, err := reader.probe(context.Background())
			if err != nil || probe.Status != test.status {
				t.Fatalf("probe=%+v err=%v", probe, err)
			}
			_, readErr := ReadSchema3(context.Background(), root, time.Now().UTC())
			switch test.status {
			case SourceLegacy:
				if !errors.Is(readErr, ErrNeedsShellUpgrade) || !strings.Contains(readErr.Error(), "sh-ver") {
					t.Fatalf("legacy error=%v", readErr)
				}
			case SourceNewer:
				if !errors.Is(readErr, ErrUnsupportedSource) {
					t.Fatalf("newer error=%v", readErr)
				}
			case SourceConflict:
				if !errors.Is(readErr, ErrInvalidSource) {
					t.Fatalf("conflict error=%v", readErr)
				}
			}
		})
	}
	t.Run("unknown", func(t *testing.T) {
		root := t.TempDir()
		reader, _ := newReader(root)
		probe, err := reader.probe(context.Background())
		if err != nil || probe.Status != SourceUnknown {
			t.Fatalf("probe=%+v err=%v", probe, err)
		}
	})
}

func TestReadSchema3RejectsCorruptAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"client-csv", func(t *testing.T, root string) {
			writeLegacy(t, root, "meta/client-ip.csv", "# id,name,ip\n"+testClientID+",wrong,\n", 0o600)
		}},
		{"audit-unknown", func(t *testing.T, root string) {
			writeLegacy(t, root, "meta/audit.jsonl", "{\"timestamp\":\"2026-07-21T00:00:00Z\",\"event\":\"unknown\",\"outcome\":\"applied\",\"client_id\":null,\"client_name\":null,\"legacy\":false}\n", 0o600)
		}},
		{"audit-duplicate", func(t *testing.T, root string) {
			writeLegacy(t, root, "meta/audit.jsonl", "{\"timestamp\":\"2026-07-21T00:00:00Z\",\"event\":\"client_ip_apply\",\"event\":\"client_ip_apply\",\"outcome\":\"applied\",\"client_id\":null,\"client_name\":null,\"legacy\":false}\n", 0o600)
		}},
		{"profile-conflict", func(t *testing.T, root string) {
			path := filepath.Join(root, "clients/active/laptop.ovpn")
			data, _ := os.ReadFile(path)
			writeLegacy(t, root, "clients/active/laptop.ovpn", strings.Replace(string(data), "# ovpn-client-name: laptop", "# ovpn-client-name: desktop", 1), 0o600)
		}},
		{"unsafe-mode", func(t *testing.T, root string) {
			if err := os.Chmod(filepath.Join(root, "meta/client-state.csv"), 0o644); err != nil {
				t.Fatal(err)
			}
		}},
		{"symlink", func(t *testing.T, root string) {
			path := filepath.Join(root, "meta/client-state.csv")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("../client-ip.csv", path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := makeLegacyFixture(t)
			test.mutate(t, fixture.root)
			if _, err := ReadSchema3(context.Background(), fixture.root, fixture.now); !errors.Is(err, ErrInvalidSource) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestReadSchema3ClassifiesDiscardableLeases(t *testing.T) {
	fixture := makeLegacyFixture(t)
	writeLegacy(t, fixture.root, "cache/client-leases/"+testClientID, "10.42.0.10\n", 0o600)
	source, err := ReadSchema3(context.Background(), fixture.root, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if !source.CanonicalClientIPs || len(source.Repairs) != 1 || source.Leases[0].Import {
		t.Fatalf("canonical=%v repairs=%+v leases=%+v", source.CanonicalClientIPs, source.Repairs, source.Leases)
	}
}

func makeLegacyFixture(t *testing.T) legacyFixture {
	t.Helper()
	root := t.TempDir()
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	for _, directory := range []string{"config", "meta", "pki/private", "pki/issued", "secrets", "clients/active", "cache/client-leases"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	caPublic, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OpenVPN Container CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	server, serverKey := makeLegacyLeaf(t, ca, caKey, "openvpn-server", 2, x509.ExtKeyUsageServerAuth, now)
	client, clientKey := makeLegacyLeaf(t, ca, caKey, testClientID, 3, x509.ExtKeyUsageClientAuth, now)
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour)}, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Raw})
	clientPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: client.Raw})
	caKeyPEM := marshalLegacyKey(t, caKey)
	serverKeyPEM := marshalLegacyKey(t, serverKey)
	clientKeyPEM := marshalLegacyKey(t, clientKey)
	tls := []byte("-----BEGIN OpenVPN Static key V1-----\n" + hex.EncodeToString(bytesOf(0x5a, 256)) + "\n-----END OpenVPN Static key V1-----\n")
	writeLegacyBytes(t, root, "pki/ca.crt", caPEM, 0o644)
	writeLegacyBytes(t, root, "pki/private/ca.key", caKeyPEM, 0o600)
	writeLegacyBytes(t, root, "pki/issued/openvpn-server.crt", serverPEM, 0o644)
	writeLegacyBytes(t, root, "pki/private/openvpn-server.key", serverKeyPEM, 0o600)
	writeLegacyBytes(t, root, "pki/issued/"+testClientID+".crt", clientPEM, 0o644)
	writeLegacyBytes(t, root, "pki/private/"+testClientID+".key", clientKeyPEM, 0o600)
	writeLegacyBytes(t, root, "pki/crl.pem", pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: crlDER}), 0o644)
	writeLegacy(t, root, "pki/index.txt", "V\t270721000000Z\t\t02\tunknown\t/CN=openvpn-server\nV\t270721000000Z\t\t03\tunknown\t/CN="+testClientID+"\n", 0o600)
	writeLegacyBytes(t, root, "secrets/tls-crypt.key", tls, 0o600)
	project := "OVPN_CONFIG_VERSION=3\nOVPN_ENDPOINT=vpn.example.com\nOVPN_PROTO=udp\nOVPN_TRANSPORT_FAMILY=auto\nOVPN_PORT=1194\nOVPN_NETWORK=10.42.0.0/24\nOVPN_TOPOLOGY=subnet\nOVPN_DYNAMIC_POOL_SIZE=64\nOVPN_NAT=false\nOVPN_NAT_INTERFACE=auto\nOVPN_REDIRECT_GATEWAY=false\nOVPN_CLIENT_TO_CLIENT=true\nOVPN_DNS=\nOVPN_ROUTES=\nOVPN_LOG_MAX_BYTES=10485760\nOVPN_LOG_BACKUPS=5\n"
	writeLegacy(t, root, "config/project.env", project, 0o600)
	writeLegacy(t, root, "config/schema-version", "3\n", 0o600)
	fingerprint := sha256.Sum256(ca.Raw)
	metadata := fmt.Sprintf("{\n  \"schema_version\": 1,\n  \"instance_id\": \"%s\",\n  \"initialized_at\": \"%s\",\n  \"server_name\": \"openvpn-server\",\n  \"data_dir\": \"/etc/openvpn\",\n  \"ca_fingerprint_sha256\": \"%s\"\n}\n", testInstanceID, now.Format(time.RFC3339), colonFingerprint(fingerprint))
	writeLegacy(t, root, "meta/instance.json", metadata, 0o600)
	writeLegacy(t, root, "meta/instance-id", testInstanceID+"\n", 0o600)
	writeLegacy(t, root, "meta/client-state.csv", "# id,name,state\n"+testClientID+",laptop,active\n", 0o600)
	writeLegacy(t, root, "meta/client-ip.csv", "# id,name,ip\n"+testClientID+",laptop,\n", 0o600)
	writeLegacy(t, root, "meta/audit.jsonl", "", 0o600)
	profile := []byte("client\n# ovpn-client-id: " + testClientID + "\n# ovpn-client-name: laptop\n<ca>\n" + strings.TrimSpace(string(caPEM)) + "\n</ca>\n<cert>\n" + strings.TrimSpace(string(clientPEM)) + "\n</cert>\n<key>\n" + strings.TrimSpace(string(clientKeyPEM)) + "\n</key>\n<tls-crypt>\n" + strings.TrimSpace(string(tls)) + "\n</tls-crypt>\n")
	writeLegacyBytes(t, root, "clients/active/laptop.ovpn", profile, 0o600)
	writeLegacy(t, root, "cache/client-leases/"+testClientID, "10.42.0.200\n", 0o600)
	return legacyFixture{root: root, now: now}
}

func makeLegacyLeaf(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, name string, serial int64, usage x509.ExtKeyUsage, now time.Time) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, public, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, _ := x509.ParseCertificate(der)
	return certificate, key
}
func marshalLegacyKey(t *testing.T, key ed25519.PrivateKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}
func bytesOf(value byte, count int) []byte {
	output := make([]byte, count)
	for index := range output {
		output[index] = value
	}
	return output
}
func colonFingerprint(value [32]byte) string {
	raw := strings.ToUpper(hex.EncodeToString(value[:]))
	parts := make([]string, 0, 32)
	for index := 0; index < len(raw); index += 2 {
		parts = append(parts, raw[index:index+2])
	}
	return strings.Join(parts, ":")
}
func writeLegacy(t *testing.T, root, key, value string, mode os.FileMode) {
	t.Helper()
	writeLegacyBytes(t, root, key, []byte(value), mode)
}
func writeLegacyBytes(t *testing.T, root, key string, value []byte, mode os.FileMode) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, value, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}
func snapshotFiles(t *testing.T, root string) []string {
	t.Helper()
	values := []string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if entry.IsDir() {
			values = append(values, "d:"+relative)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		values = append(values, fmt.Sprintf("f:%s:%04o:%x", relative, info.Mode().Perm(), sum))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return values
}
