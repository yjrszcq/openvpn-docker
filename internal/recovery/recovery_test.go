package recovery

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const (
	recoveryInstanceID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	recoveryClientID   = "11111111-1111-4111-8111-111111111111"
)

func TestRecoverProfileBackedArtifacts(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ca, caKey := recoveryCA(t, now)
	certificate, privateKey := recoveryClient(t, ca, caKey, now)
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw})
	keyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	tlsPEM := []byte("-----BEGIN OpenVPN Static key V1-----\n" + hex.EncodeToString(bytesOf(0x5a, 256)) + "\n-----END OpenVPN Static key V1-----\n")
	profile := recoveryProfile("laptop", caPEM, certPEM, keyPEM, tlsPEM)

	dataDir := filepath.Join(t.TempDir(), "openvpn")
	if err := os.MkdirAll(filepath.Join(dataDir, "clients", "active"), 0o750); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(dataDir, "clients", "active", "laptop.ovpn")
	if err := os.WriteFile(profilePath, profile, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := storesqlite.Create(ctx, filepath.Join(dataDir, "meta", "state.db"), "4.0.0-test")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	config, err := configservice.Parse([]byte("version: 1\nserver: {endpoint: vpn.example.test}\nipv4: {network: 10.42.0.0/24}\n"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := configservice.NewAppliedSnapshot(1, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateInstance(ctx, storesqlite.InstanceState{ID: recoveryInstanceID, CreatedAt: now, CAFingerprint: sha256.Sum256(ca.Raw), Applied: snapshot}); err != nil {
		t.Fatal(err)
	}
	profileMetadata := storesqlite.ArtifactMetadata{ID: "22222222-2222-4222-8222-222222222222", OwnerKind: "client", OwnerID: recoveryClientID, Kind: "profile", Key: "clients/active/laptop.ovpn", Digest: sha256.Sum256(profile), Status: storesqlite.ArtifactActive}
	if err := store.CreateClient(ctx, recoveryInstanceID, storesqlite.ClientState{Client: domain.Client{ID: recoveryClientID, Name: "laptop", Status: domain.ClientActive}, CreatedAt: now, Artifacts: []storesqlite.ArtifactMetadata{profileMetadata}}); err != nil {
		t.Fatal(err)
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store, local, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	report := statecontrol.Report{Issues: []statecontrol.Issue{
		{Severity: statecontrol.SeverityRecoverable, Action: "RECOVER_CA_CERT"},
		{Severity: statecontrol.SeverityRecoverable, Action: "RECOVER_TLS_CRYPT"},
		{Severity: statecontrol.SeverityRecoverable, Action: "RECOVER_CLIENT_IDENTITY", OwnerID: recoveryClientID},
	}}
	if err := os.WriteFile(profilePath, append(append([]byte(nil), profile...), []byte("# untracked change\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	assessment, err := service.Assess(ctx, report)
	if err != nil || len(assessment.Ready) != 0 {
		t.Fatalf("untracked profile assessment=%+v err=%v", assessment, err)
	}
	if err := os.WriteFile(profilePath, profile, 0o600); err != nil {
		t.Fatal(err)
	}
	assessment, err = service.Assess(ctx, report)
	if err != nil || len(assessment.Ready) != 3 {
		t.Fatalf("assessment=%+v err=%v", assessment, err)
	}
	for _, candidate := range assessment.Ready {
		if _, err := service.Recover(ctx, recoveryInstanceID, candidate.Action, candidate.OwnerID); err != nil {
			t.Fatalf("recover %s: %v", candidate.Action, err)
		}
	}
	if _, err := pki.ValidateCA(dataDir+"/pki", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pki.ValidateClient(dataDir+"/pki", recoveryClientID, now); err != nil {
		t.Fatal(err)
	}
	if err := pki.ValidateTLSCryptKey(dataDir + "/secrets/tls-crypt.key"); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadClient(ctx, recoveryInstanceID, recoveryClientID)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, item := range loaded.Artifacts {
		if item.Status == storesqlite.ArtifactActive {
			kinds[item.Kind] = true
		}
	}
	for _, kind := range []string{"profile", "client-cert", "client-key"} {
		if !kinds[kind] {
			t.Fatalf("active artifact kinds=%v", kinds)
		}
	}
}

func TestProfileEvidenceRejectsIdentityAndConsensusConflicts(t *testing.T) {
	if _, err := parseProfile([]byte("# ovpn-client-id: wrong\n"), "profile", recoveryClientID, "laptop", time.Now()); err == nil {
		t.Fatal("mismatched profile identity was accepted")
	}
	values := []profileEvidence{{path: "one", tlsCrypt: []byte("one")}, {path: "two", tlsCrypt: []byte("two")}}
	if _, ok := consensus(values, func(value profileEvidence) []byte { return value.tlsCrypt }, nil); ok {
		t.Fatal("conflicting recovery evidence reached consensus")
	}
}

func recoveryCA(t *testing.T, now time.Time) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OpenVPN Container CA"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, private
}

func recoveryClient(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, now time.Time) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: recoveryClientID}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, public, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, private
}

func recoveryProfile(name string, ca, cert, key, tls []byte) []byte {
	return []byte("client\n# ovpn-client-id: " + recoveryClientID + "\n# ovpn-client-name: " + name + "\n<ca>\n" + strings.TrimSpace(string(ca)) + "\n</ca>\n<cert>\n" + strings.TrimSpace(string(cert)) + "\n</cert>\n<key>\n" + strings.TrimSpace(string(key)) + "\n</key>\n<tls-crypt>\n" + strings.TrimSpace(string(tls)) + "\n</tls-crypt>\n")
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
