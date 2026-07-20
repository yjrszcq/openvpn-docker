package pki

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
	testServerName = "openvpn-server"
	testClientID   = "51515151-5151-4515-8515-515151515151"
)

type cryptoFixture struct {
	now       time.Time
	ca        *x509.Certificate
	caKey     ed25519.PrivateKey
	server    *x509.Certificate
	serverKey ed25519.PrivateKey
	client    *x509.Certificate
	clientKey ed25519.PrivateKey
	crl       []byte
}

func makeCryptoFixture(t *testing.T) cryptoFixture {
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
	server, serverKey := makeLeaf(t, ca, caKey, testServerName, 2, x509.ExtKeyUsageServerAuth, now)
	client, clientKey := makeLeaf(t, ca, caKey, testClientID, 3, x509.ExtKeyUsageClientAuth, now)
	crlDER, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: now.Add(-time.Minute), NextUpdate: now.Add(time.Hour),
	}, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return cryptoFixture{now: now, ca: ca, caKey: caKey, server: server, serverKey: serverKey, client: client, clientKey: clientKey, crl: crlDER}
}

func makeLeaf(t *testing.T, ca *x509.Certificate, caKey ed25519.PrivateKey, name string, serial int64, usage x509.ExtKeyUsage, now time.Time) (*x509.Certificate, ed25519.PrivateKey) {
	t.Helper()
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, public, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, key
}

func writeFixture(t *testing.T, directory string, fixture cryptoFixture) {
	t.Helper()
	for _, path := range []string{filepath.Join(directory, "private"), filepath.Join(directory, "issued")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeCertificate(t, filepath.Join(directory, "ca.crt"), fixture.ca)
	writePrivateKey(t, filepath.Join(directory, "private", "ca.key"), fixture.caKey)
	writeCertificate(t, filepath.Join(directory, "issued", testServerName+".crt"), fixture.server)
	writePrivateKey(t, filepath.Join(directory, "private", testServerName+".key"), fixture.serverKey)
	writeCertificate(t, filepath.Join(directory, "issued", testClientID+".crt"), fixture.client)
	writePrivateKey(t, filepath.Join(directory, "private", testClientID+".key"), fixture.clientKey)
	if err := os.WriteFile(filepath.Join(directory, "crl.pem"), pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: fixture.crl}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCertificate(t *testing.T, path string, certificate *x509.Certificate) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writePrivateKey(t *testing.T, path string, key ed25519.PrivateKey) {
	t.Helper()
	encoded, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAuthorityAndClient(t *testing.T) {
	fixture := makeCryptoFixture(t)
	directory := filepath.Join(t.TempDir(), "pki")
	writeFixture(t, directory, fixture)
	authority, err := ValidateAuthority(directory, testServerName, fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if authority.CA.Serial != "1" || authority.Server.Serial != "2" || authority.CA.Fingerprint != sha256.Sum256(fixture.ca.Raw) {
		t.Fatalf("unexpected authority: %+v", authority)
	}
	client, err := ValidateClient(directory, testClientID, fixture.now)
	if err != nil || client.Serial != "3" || client.Fingerprint != sha256.Sum256(fixture.client.Raw) {
		t.Fatalf("unexpected client: %+v err=%v", client, err)
	}
}

func TestCryptoValidationRejectsMismatchIdentityAndExpiry(t *testing.T) {
	fixture := makeCryptoFixture(t)
	t.Run("key-mismatch", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "pki")
		writeFixture(t, directory, fixture)
		writePrivateKey(t, filepath.Join(directory, "private", testServerName+".key"), fixture.clientKey)
		if _, err := ValidateAuthority(directory, testServerName, fixture.now); !errors.Is(err, ErrInvalidMaterial) {
			t.Fatalf("key mismatch error=%v", err)
		}
	})
	t.Run("client-common-name", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "pki")
		writeFixture(t, directory, fixture)
		other := "52525252-5252-4525-8525-525252525252"
		writeCertificate(t, filepath.Join(directory, "issued", other+".crt"), fixture.client)
		writePrivateKey(t, filepath.Join(directory, "private", other+".key"), fixture.clientKey)
		if _, err := ValidateClient(directory, other, fixture.now); !errors.Is(err, ErrInvalidMaterial) {
			t.Fatalf("common-name error=%v", err)
		}
	})
	t.Run("expired-crl", func(t *testing.T) {
		directory := filepath.Join(t.TempDir(), "pki")
		writeFixture(t, directory, fixture)
		if err := ValidateCRL(filepath.Join(directory, "crl.pem"), fixture.ca, fixture.now.Add(2*time.Hour)); !errors.Is(err, ErrInvalidMaterial) {
			t.Fatalf("expired CRL error=%v", err)
		}
	})
}

func TestValidateTLSCryptKey(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.key")
	encoded := hex.EncodeToString(bytesRepeat(0x5a, 256))
	content := "# generated\n-----BEGIN OpenVPN Static key V1-----\n" + encoded + "\n-----END OpenVPN Static key V1-----\n"
	if err := os.WriteFile(valid, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTLSCryptKey(valid); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"short":    strings.Replace(content, encoded, "abcd", 1),
		"non-hex":  strings.Replace(content, encoded[:1], "z", 1),
		"all-zero": strings.Replace(content, encoded, strings.Repeat("0", 512), 1),
	} {
		path := filepath.Join(directory, name+".key")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := ValidateTLSCryptKey(path); !errors.Is(err, ErrInvalidMaterial) {
			t.Errorf("%s error=%v", name, err)
		}
	}
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}

type fakeExecutor struct {
	calls   []Invocation
	handler func(Invocation) error
}

func (executor *fakeExecutor) Run(_ context.Context, invocation Invocation) error {
	executor.calls = append(executor.calls, invocation)
	if executor.handler != nil {
		return executor.handler(invocation)
	}
	return nil
}

func TestRunnerInitializeUsesControlledCommandContract(t *testing.T) {
	fixture := makeCryptoFixture(t)
	pkiDir := filepath.Join(t.TempDir(), "stage", "pki")
	executor := &fakeExecutor{}
	executor.handler = func(invocation Invocation) error {
		switch invocation.Args[0] {
		case "init-pki":
			return os.MkdirAll(filepath.Join(pkiDir, "private"), 0o700)
		case "build-ca":
			writeCertificate(t, filepath.Join(pkiDir, "ca.crt"), fixture.ca)
			writePrivateKey(t, filepath.Join(pkiDir, "private", "ca.key"), fixture.caKey)
		case "build-server-full":
			if err := os.MkdirAll(filepath.Join(pkiDir, "issued"), 0o700); err != nil {
				return err
			}
			writeCertificate(t, filepath.Join(pkiDir, "issued", testServerName+".crt"), fixture.server)
			writePrivateKey(t, filepath.Join(pkiDir, "private", testServerName+".key"), fixture.serverKey)
		case "gen-crl":
			return os.WriteFile(filepath.Join(pkiDir, "crl.pem"), pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: fixture.crl}), 0o644)
		}
		return nil
	}
	runner, err := NewRunner(Config{EasyRSABinary: "fake-easyrsa", OpenVPNBinary: "fake-openvpn"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	runner.now = func() time.Time { return fixture.now }
	t.Setenv("SHOULD_NOT_LEAK", "secret")
	authority, err := runner.Initialize(context.Background(), pkiDir, testServerName)
	if err != nil || authority.Server.Serial != "2" || len(executor.calls) != 4 {
		t.Fatalf("initialize authority=%+v calls=%d err=%v", authority, len(executor.calls), err)
	}
	for _, call := range executor.calls {
		joined := strings.Join(call.Env, "\n")
		if strings.Contains(joined, "SHOULD_NOT_LEAK") || !strings.Contains(joined, "EASYRSA_BATCH=1") || !strings.Contains(joined, "EASYRSA_PKI="+pkiDir) {
			t.Fatalf("uncontrolled environment: %v", call.Env)
		}
	}
	if !containsEnv(executor.calls[1].Env, "EASYRSA_REQ_CN=OpenVPN Container CA") || !containsEnv(executor.calls[2].Env, "EASYRSA_REQ_CN="+testServerName) {
		t.Fatalf("missing request common names: %+v", executor.calls)
	}
}

func TestRunnerGeneratesTLSCryptAtomically(t *testing.T) {
	output := filepath.Join(t.TempDir(), "secrets", "tls-crypt.key")
	executor := &fakeExecutor{handler: func(invocation Invocation) error {
		encoded := hex.EncodeToString(bytesRepeat(0x6b, 256))
		content := "-----BEGIN OpenVPN Static key V1-----\n" + encoded + "\n-----END OpenVPN Static key V1-----\n"
		return os.WriteFile(invocation.Args[2], []byte(content), 0o666)
	}}
	runner, err := NewRunner(Config{EasyRSABinary: "fake-easyrsa", OpenVPNBinary: "fake-openvpn"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.GenerateTLSCrypt(context.Background(), output); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("tls-crypt mode=%v", info.Mode().Perm())
	}
	if err := runner.GenerateTLSCrypt(context.Background(), output); err == nil {
		t.Fatal("tls-crypt generation overwrote an existing key")
	}
}

func TestRunnerIssuesAndRevokesClient(t *testing.T) {
	fixture := makeCryptoFixture(t)
	pkiDir := filepath.Join(t.TempDir(), "pki")
	writeFixture(t, pkiDir, fixture)
	executor := &fakeExecutor{handler: func(invocation Invocation) error {
		switch invocation.Args[0] {
		case "build-client-full":
			writeCertificate(t, filepath.Join(pkiDir, "issued", testClientID+".crt"), fixture.client)
			writePrivateKey(t, filepath.Join(pkiDir, "private", testClientID+".key"), fixture.clientKey)
		case "gen-crl":
			return os.WriteFile(filepath.Join(pkiDir, "crl.pem"), pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: fixture.crl}), 0o644)
		}
		return nil
	}}
	runner, err := NewRunner(Config{EasyRSABinary: "fake-easyrsa", OpenVPNBinary: "fake-openvpn"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	runner.now = func() time.Time { return fixture.now }
	info, err := runner.IssueClient(context.Background(), pkiDir, testClientID)
	if err != nil || info.Serial != "3" {
		t.Fatalf("issued client=%+v err=%v", info, err)
	}
	if err := runner.RevokeClient(context.Background(), pkiDir, testClientID); err != nil {
		t.Fatal(err)
	}
	wantCommands := []string{"build-client-full", "revoke", "gen-crl"}
	if len(executor.calls) != len(wantCommands) {
		t.Fatalf("calls=%+v", executor.calls)
	}
	for index, want := range wantCommands {
		if executor.calls[index].Args[0] != want {
			t.Fatalf("call %d=%v, want %s", index, executor.calls[index].Args, want)
		}
	}
}

func TestRunnerStopsOnExternalFailure(t *testing.T) {
	executor := &fakeExecutor{handler: func(Invocation) error { return fmt.Errorf("%w: injected", ErrCommand) }}
	runner, err := NewRunner(Config{EasyRSABinary: "fake-easyrsa", OpenVPNBinary: "fake-openvpn"}, executor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Initialize(context.Background(), filepath.Join(t.TempDir(), "pki"), testServerName); !errors.Is(err, ErrCommand) {
		t.Fatalf("external failure=%v", err)
	}
	if len(executor.calls) != 1 {
		t.Fatalf("commands continued after failure: %d", len(executor.calls))
	}
}

func TestExecExecutorClassifiesUnavailableAndFailure(t *testing.T) {
	executor := ExecExecutor{}
	if err := executor.Run(context.Background(), Invocation{Path: "definitely-missing-ovpn-tool", Env: controlledEnvironment(nil)}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable error=%v", err)
	}
	if err := executor.Run(context.Background(), Invocation{Path: "sh", Args: []string{"-c", "printf failure >&2; exit 7"}, Env: controlledEnvironment(nil)}); !errors.Is(err, ErrCommand) || !strings.Contains(err.Error(), "failure") {
		t.Fatalf("command failure=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := executor.Run(canceled, Invocation{Path: "sh", Env: controlledEnvironment(nil)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command error=%v", err)
	}
}

func containsEnv(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
