package initialize

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
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

	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

const testYAML = `version: 1
server:
  endpoint: vpn.example.test
ipv4:
  network: 10.42.0.0/24
`

type generatedPKI struct {
	now              time.Time
	ca, server       *x509.Certificate
	caKey, serverKey ed25519.PrivateKey
	crl              []byte
}

func generatePKI(t *testing.T) generatedPKI {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	caPublic, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "OpenVPN Container CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true,
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caPublic, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	serverPublic, serverKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: DefaultServerName},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, ca, serverPublic, caKey)
	if err != nil {
		t.Fatal(err)
	}
	server, err := x509.ParseCertificate(serverDER)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour)}, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return generatedPKI{now: now, ca: ca, server: server, caKey: caKey, serverKey: serverKey, crl: crl}
}

type initializationExecutor struct {
	t       *testing.T
	fixture generatedPKI
	fail    string
	calls   int
}

func (executor *initializationExecutor) Run(_ context.Context, invocation pki.Invocation) error {
	executor.calls++
	if len(invocation.Args) == 0 {
		return fmt.Errorf("missing command")
	}
	if invocation.Args[0] == executor.fail {
		return fmt.Errorf("%w: injected %s", pki.ErrCommand, executor.fail)
	}
	if invocation.Args[0] == "--genkey" {
		encoded := hex.EncodeToString(bytesOf(0x7c, 256))
		content := "-----BEGIN OpenVPN Static key V1-----\n" + encoded + "\n-----END OpenVPN Static key V1-----\n"
		return os.WriteFile(invocation.Args[2], []byte(content), 0o666)
	}
	pkiDir := environmentValue(invocation.Env, "EASYRSA_PKI")
	switch invocation.Args[0] {
	case "init-pki":
		return os.MkdirAll(filepath.Join(pkiDir, "private"), 0o700)
	case "build-ca":
		writeCert(executor.t, filepath.Join(pkiDir, "ca.crt"), executor.fixture.ca)
		writeKey(executor.t, filepath.Join(pkiDir, "private", "ca.key"), executor.fixture.caKey)
	case "build-server-full":
		if err := os.MkdirAll(filepath.Join(pkiDir, "issued"), 0o700); err != nil {
			return err
		}
		writeCert(executor.t, filepath.Join(pkiDir, "issued", DefaultServerName+".crt"), executor.fixture.server)
		writeKey(executor.t, filepath.Join(pkiDir, "private", DefaultServerName+".key"), executor.fixture.serverKey)
	case "gen-crl":
		return os.WriteFile(filepath.Join(pkiDir, "crl.pem"), pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: executor.fixture.crl}), 0o644)
	default:
		return fmt.Errorf("unexpected command %v", invocation.Args)
	}
	return nil
}

func newInitializationService(t *testing.T, fail string) (*Service, *initializationExecutor) {
	t.Helper()
	fixture := generatePKI(t)
	executor := &initializationExecutor{t: t, fixture: fixture, fail: fail}
	runner, err := pki.NewRunner(pki.Config{EasyRSABinary: "fake-easyrsa", OpenVPNBinary: "fake-openvpn"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := compatibility.Load(filepath.Join("..", "..", "compatibility", "contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := render.New(filepath.Join("..", "..", "rootfs", "usr", "local", "share", "openvpn-container", "templates"), contract)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(runner, renderer)
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return fixture.now }
	return service, executor
}

func initializationOptions(t *testing.T) Options {
	t.Helper()
	root := t.TempDir()
	configFile := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(configFile, []byte(testYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	return Options{DataDir: filepath.Join(root, "data"), RuntimeDir: filepath.Join(root, "run"), ConfigFile: configFile, ServerName: DefaultServerName, Version: "4.0.0-test"}
}

func TestInitializeCreatesCompleteSchemaFourInstance(t *testing.T) {
	service, _ := newInitializationService(t, "")
	options := initializationOptions(t)
	result, err := service.Initialize(context.Background(), options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.InstanceID == "" || result.OperationID == "" || result.Recovered {
		t.Fatalf("unexpected result: %+v", result)
	}
	assertHealthyInstance(t, service, options, result)
	if _, err := service.Initialize(context.Background(), options, nil); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("repeated initialization error=%v", err)
	}
}

func TestInitializeFailureLeavesOnlyTheLock(t *testing.T) {
	service, _ := newInitializationService(t, "build-server-full")
	options := initializationOptions(t)
	if _, err := service.Initialize(context.Background(), options, nil); !errors.Is(err, pki.ErrCommand) {
		t.Fatalf("initialization failure=%v", err)
	}
	entries, err := os.ReadDir(options.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".ovpn-data.lock" {
		t.Fatalf("failed initialization leftovers=%v", entryNames(entries))
	}
}

func TestInitializationCrashPointsRecoverDeterministically(t *testing.T) {
	points := []CrashPoint{CrashAfterStaged, CrashAfterMarkerPrepared, CrashAfterEntryMoved, CrashAfterFilesInstalled, CrashAfterCommitted}
	for _, point := range points {
		t.Run(string(point), func(t *testing.T) {
			service, _ := newInitializationService(t, "")
			options := initializationOptions(t)
			injected := errors.New("simulated crash")
			fired := false
			_, err := service.Initialize(context.Background(), options, func(got CrashPoint) error {
				if got == point && !fired {
					fired = true
					return injected
				}
				return nil
			})
			if !errors.Is(err, injected) || !fired {
				t.Fatalf("crash point %s error=%v fired=%t", point, err, fired)
			}
			recovered, err := service.Recover(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			if point == CrashAfterStaged {
				if recovered.InstanceID != "" || !recovered.Recovered {
					t.Fatalf("staging recovery=%+v", recovered)
				}
				recovered, err = service.Initialize(context.Background(), options, nil)
				if err != nil {
					t.Fatal(err)
				}
			} else if !recovered.Recovered {
				t.Fatalf("recovery result=%+v", recovered)
			}
			assertHealthyInstance(t, service, options, recovered)
		})
	}
}

func assertHealthyInstance(t *testing.T, service *Service, options Options, result Result) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(options.DataDir, markerName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("initialization marker remains: %v", err)
	}
	stages, err := filepath.Glob(filepath.Join(options.DataDir, ".init-*"))
	if err != nil || len(stages) != 0 {
		t.Fatalf("initialization stages=%v err=%v", stages, err)
	}
	database, err := storesqlite.Open(context.Background(), filepath.Join(options.DataDir, "meta", "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	state, err := database.LoadInstance(context.Background(), result.InstanceID)
	if err != nil || state.Applied.Config.Endpoint != "vpn.example.test" || state.Applied.Revision != 1 {
		t.Fatalf("instance state=%+v err=%v", state, err)
	}
	operation, err := database.LoadOperation(context.Background(), result.OperationID)
	if err != nil || operation.State != storesqlite.OperationCommitted {
		t.Fatalf("operation=%+v err=%v", operation, err)
	}
	artifacts, err := database.LoadInstanceArtifacts(context.Background(), result.InstanceID)
	if err != nil || len(artifacts) != 7 {
		t.Fatalf("artifacts=%d err=%v", len(artifacts), err)
	}
	server, err := os.ReadFile(filepath.Join(options.DataDir, "server", "server.conf"))
	if err != nil || !strings.Contains(string(server), "ca "+filepath.Join(options.DataDir, "pki", "ca.crt")) {
		t.Fatalf("server config error=%v\n%s", err, server)
	}
	if err := service.validateInstance(context.Background(), options.DataDir, options, result.InstanceID); err != nil {
		t.Fatal(err)
	}
}

func writeCert(t *testing.T, path string, certificate *x509.Certificate) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeKey(t *testing.T, path string, key ed25519.PrivateKey) {
	t.Helper()
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func environmentValue(values []string, key string) string {
	for _, value := range values {
		if strings.HasPrefix(value, key+"=") {
			return strings.TrimPrefix(value, key+"=")
		}
	}
	return ""
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

func entryNames(entries []os.DirEntry) []string {
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		values = append(values, entry.Name())
	}
	return values
}
