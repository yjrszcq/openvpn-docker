package pki

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
)

var (
	ErrUnavailable = errors.New("PKI dependency is unavailable")
	ErrCommand     = errors.New("PKI command failed")
	identityName   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
)

type Invocation struct {
	Path string
	Args []string
	Env  []string
}

type Executor interface {
	Run(context.Context, Invocation) error
}

type ExecExecutor struct{}

type Runner struct {
	easyRSA  string
	openVPN  string
	executor Executor
	now      func() time.Time
}

type Config struct {
	EasyRSABinary string
	OpenVPNBinary string
}

func NewRunner(config Config, executor Executor) (*Runner, error) {
	if err := validateBinary(config.EasyRSABinary); err != nil {
		return nil, fmt.Errorf("Easy-RSA binary: %w", err)
	}
	if err := validateBinary(config.OpenVPNBinary); err != nil {
		return nil, fmt.Errorf("OpenVPN binary: %w", err)
	}
	if executor == nil {
		executor = ExecExecutor{}
	}
	return &Runner{easyRSA: config.EasyRSABinary, openVPN: config.OpenVPNBinary, executor: executor, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (runner *Runner) Initialize(ctx context.Context, pkiDir, serverName string) (Authority, error) {
	if err := validatePKIPath(pkiDir); err != nil {
		return Authority{}, err
	}
	if !identityName.MatchString(serverName) {
		return Authority{}, fmt.Errorf("invalid server identity")
	}
	if err := os.MkdirAll(filepath.Dir(pkiDir), 0o700); err != nil {
		return Authority{}, err
	}
	if info, err := os.Lstat(pkiDir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return Authority{}, fmt.Errorf("PKI directory is unsafe")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Authority{}, err
	}
	for _, command := range []struct {
		args []string
		cn   string
	}{
		{args: []string{"init-pki"}},
		{args: []string{"build-ca", "nopass"}, cn: "OpenVPN Container CA"},
		{args: []string{"build-server-full", serverName, "nopass"}, cn: serverName},
		{args: []string{"gen-crl"}},
	} {
		if err := runner.runEasyRSA(ctx, pkiDir, command.cn, command.args...); err != nil {
			return Authority{}, err
		}
	}
	if err := enforceAuthorityModes(pkiDir, serverName); err != nil {
		return Authority{}, err
	}
	return ValidateAuthority(pkiDir, serverName, runner.now())
}

func (runner *Runner) IssueClient(ctx context.Context, pkiDir, clientID string) (CertificateInfo, error) {
	if err := validatePKIPath(pkiDir); err != nil {
		return CertificateInfo{}, err
	}
	if !domain.ValidUUID(clientID) {
		return CertificateInfo{}, fmt.Errorf("invalid client UUID")
	}
	if err := runner.runEasyRSA(ctx, pkiDir, clientID, "build-client-full", clientID, "nopass"); err != nil {
		return CertificateInfo{}, err
	}
	if err := os.Chmod(filepath.Join(pkiDir, "issued", clientID+".crt"), 0o644); err != nil {
		return CertificateInfo{}, fmt.Errorf("set client certificate mode: %w", err)
	}
	if err := os.Chmod(filepath.Join(pkiDir, "private", clientID+".key"), 0o600); err != nil {
		return CertificateInfo{}, fmt.Errorf("set client key mode: %w", err)
	}
	return ValidateClient(pkiDir, clientID, runner.now())
}

func (runner *Runner) RevokeClient(ctx context.Context, pkiDir, clientID string) error {
	if err := validatePKIPath(pkiDir); err != nil {
		return err
	}
	if !domain.ValidUUID(clientID) {
		return fmt.Errorf("invalid client UUID")
	}
	if err := runner.runEasyRSA(ctx, pkiDir, "", "revoke", clientID); err != nil {
		return err
	}
	return runner.GenerateCRL(ctx, pkiDir)
}

// ReissueClient replaces the request, private key, and certificate for a
// previously revoked common name. Easy-RSA's index remains the signing
// authority; callers must revoke an active certificate before invoking it.
func (runner *Runner) ReissueClient(ctx context.Context, pkiDir, clientID string) (CertificateInfo, error) {
	if err := validatePKIPath(pkiDir); err != nil {
		return CertificateInfo{}, err
	}
	if !domain.ValidUUID(clientID) {
		return CertificateInfo{}, fmt.Errorf("invalid client UUID")
	}
	for _, relative := range []string{
		filepath.Join("issued", clientID+".crt"),
		filepath.Join("reqs", clientID+".req"),
		filepath.Join("private", clientID+".key"),
	} {
		if err := os.Remove(filepath.Join(pkiDir, relative)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return CertificateInfo{}, fmt.Errorf("remove old client material: %w", err)
		}
	}
	return runner.IssueClient(ctx, pkiDir, clientID)
}

func (runner *Runner) GenerateCRL(ctx context.Context, pkiDir string) error {
	if err := validatePKIPath(pkiDir); err != nil {
		return err
	}
	if err := runner.runEasyRSA(ctx, pkiDir, "", "gen-crl"); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Join(pkiDir, "crl.pem"), 0o644); err != nil {
		return fmt.Errorf("set CRL mode: %w", err)
	}
	ca, err := readCertificate(filepath.Join(pkiDir, "ca.crt"))
	if err != nil {
		return err
	}
	return ValidateCRL(filepath.Join(pkiDir, "crl.pem"), ca, runner.now())
}

func (runner *Runner) GenerateTLSCrypt(ctx context.Context, filePath string) error {
	if filePath == "" || !filepath.IsAbs(filePath) || filepath.Clean(filePath) != filePath || strings.ContainsRune(filePath, '\x00') {
		return fmt.Errorf("tls-crypt path must be clean and absolute")
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0o700); err != nil {
		return err
	}
	temporary := filePath + ".tmp"
	if err := os.Remove(temporary); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	invocation := Invocation{Path: runner.openVPN, Args: []string{"--genkey", "secret", temporary}, Env: controlledEnvironment(nil)}
	if err := runner.executor.Run(ctx, invocation); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := ValidateTLSCryptKey(temporary); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if _, err := os.Lstat(filePath); err == nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("refusing to overwrite tls-crypt key")
	} else if !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(temporary)
		return err
	}
	file, err := os.Open(temporary)
	if err != nil {
		_ = os.Remove(temporary)
		return err
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(temporary)
		return errors.Join(syncErr, closeErr)
	}
	if err := os.Rename(temporary, filePath); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	parent, err := os.Open(filepath.Dir(filePath))
	if err != nil {
		return err
	}
	syncErr = parent.Sync()
	closeErr = parent.Close()
	return errors.Join(syncErr, closeErr)
}

func (runner *Runner) runEasyRSA(ctx context.Context, pkiDir, commonName string, args ...string) error {
	overrides := map[string]string{"EASYRSA_BATCH": "1", "EASYRSA_PKI": pkiDir}
	if commonName != "" {
		overrides["EASYRSA_REQ_CN"] = commonName
	}
	return runner.executor.Run(ctx, Invocation{Path: runner.easyRSA, Args: args, Env: controlledEnvironment(overrides)})
}

func (ExecExecutor) Run(ctx context.Context, invocation Invocation) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	resolved, err := exec.LookPath(invocation.Path)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrUnavailable, invocation.Path)
	}
	command := exec.CommandContext(ctx, resolved, invocation.Args...)
	command.Env = invocation.Env
	output := &cappedBuffer{limit: 64 << 10}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		message := strings.TrimSpace(output.String())
		if message == "" {
			message = err.Error()
		}
		return fmt.Errorf("%w: %s %s: %s", ErrCommand, filepath.Base(invocation.Path), strings.Join(invocation.Args, " "), message)
	}
	return nil
}

type cappedBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (buffer *cappedBuffer) Write(value []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := buffer.limit - len(buffer.data)
	if remaining > 0 {
		if remaining > len(value) {
			remaining = len(value)
		}
		buffer.data = append(buffer.data, value[:remaining]...)
	}
	return len(value), nil
}

func (buffer *cappedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return string(buffer.data)
}

func controlledEnvironment(overrides map[string]string) []string {
	values := []string{"LANG=C", "LC_ALL=C", "TZ=UTC"}
	if pathValue := os.Getenv("PATH"); pathValue != "" {
		values = append(values, "PATH="+pathValue)
	}
	keys := []string{"EASYRSA_BATCH", "EASYRSA_PKI", "EASYRSA_REQ_CN"}
	for _, key := range keys {
		if value, exists := overrides[key]; exists {
			values = append(values, key+"="+value)
		}
	}
	return values
}

func validateBinary(value string) error {
	if value == "" || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("binary path is invalid")
	}
	return nil
}

func validatePKIPath(value string) error {
	if value == "" || !filepath.IsAbs(value) || filepath.Clean(value) != value || strings.ContainsRune(value, '\x00') {
		return fmt.Errorf("PKI directory must be clean and absolute")
	}
	return nil
}

func enforceAuthorityModes(pkiDir, serverName string) error {
	for _, item := range []struct {
		path string
		mode os.FileMode
	}{
		{filepath.Join(pkiDir, "ca.crt"), 0o644},
		{filepath.Join(pkiDir, "private", "ca.key"), 0o600},
		{filepath.Join(pkiDir, "issued", serverName+".crt"), 0o644},
		{filepath.Join(pkiDir, "private", serverName+".key"), 0o600},
		{filepath.Join(pkiDir, "crl.pem"), 0o644},
	} {
		if err := os.Chmod(item.path, item.mode); err != nil {
			return fmt.Errorf("set PKI artifact mode: %w", err)
		}
	}
	return nil
}

var _ io.Writer = (*cappedBuffer)(nil)
