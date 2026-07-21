// Package network reconciles the single-node IPv4 forwarding data plane using
// instance-owned iptables chains and comments.
package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/ipam"
)

var ErrUnavailable = errors.New("IPv4 network dependency is unavailable")

var interfacePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,14}$`)

type CommandRunner interface {
	Run(context.Context, string, ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message != "" {
			return "", fmt.Errorf("%w: %s %s: %v: %s", ErrUnavailable, name, strings.Join(args, " "), err, message)
		}
		return "", fmt.Errorf("%w: %s %s: %v", ErrUnavailable, name, strings.Join(args, " "), err)
	}
	return string(output), nil
}

type Config struct {
	IPBinary       string
	IPTablesBinary string
	ForwardingFile string
	Runner         CommandRunner
}

type Reconciler struct {
	ipBinary       string
	iptablesBinary string
	forwardingFile string
	runner         CommandRunner
}

func New(config Config) (*Reconciler, error) {
	if config.IPBinary == "" || config.IPTablesBinary == "" || config.ForwardingFile == "" || !filepath.IsAbs(config.ForwardingFile) || filepath.Clean(config.ForwardingFile) != config.ForwardingFile || strings.ContainsAny(config.ForwardingFile, "\x00\r\n") {
		return nil, fmt.Errorf("IPv4 network tool configuration is invalid")
	}
	if config.Runner == nil {
		config.Runner = ExecRunner{}
	}
	return &Reconciler{ipBinary: config.IPBinary, iptablesBinary: config.IPTablesBinary, forwardingFile: config.ForwardingFile, runner: config.Runner}, nil
}

func (reconciler *Reconciler) Reconcile(ctx context.Context, instanceID string, config domain.Config) (resultErr error) {
	filterChain, natChain, comment, err := identities(instanceID)
	if err != nil {
		return err
	}
	if config.IPv4.Network.Family() != domain.FamilyIPv4 {
		return fmt.Errorf("IPv4 tunnel network is invalid")
	}
	if _, err := ipam.NewIPv4Layout(config.IPv4.Network, config.IPv4.DynamicPoolSize); err != nil {
		return err
	}
	required := config.IPv4.NATEnabled || config.IPv4.RedirectGateway || len(config.IPv4.Routes) > 0
	if !required {
		return reconciler.Cleanup(ctx, instanceID)
	}
	// Remove only this instance's prior revision before constructing the new
	// rules. A partial reconstruction is removed again on error.
	if err := reconciler.Cleanup(ctx, instanceID); err != nil {
		return err
	}
	defer func() {
		if resultErr != nil {
			cleanupContext, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = reconciler.Cleanup(cleanupContext, instanceID)
		}
	}()
	if err := reconciler.enableForwarding(); err != nil {
		return err
	}
	iface, err := reconciler.egressInterface(ctx, config.IPv4.NATInterface)
	if err != nil {
		return err
	}
	network := config.IPv4.Network.String()
	if err := reconciler.prepareChain(ctx, "filter", filterChain, comment+":filter-owner"); err != nil {
		return err
	}
	for index, rule := range [][]string{
		{"-s", network, "-o", iface, "-m", "comment", "--comment", comment + ":egress", "-j", "ACCEPT"},
		{"-d", network, "-i", iface, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-m", "comment", "--comment", comment + ":return", "-j", "ACCEPT"},
	} {
		if err := reconciler.insert(ctx, "filter", filterChain, index+1, rule...); err != nil {
			return err
		}
	}
	if err := reconciler.ensureJump(ctx, "filter", "FORWARD", filterChain, comment+":forward"); err != nil {
		return err
	}
	if !config.IPv4.NATEnabled {
		return nil
	}
	if err := reconciler.prepareChain(ctx, "nat", natChain, comment+":nat-owner"); err != nil {
		return err
	}
	if err := reconciler.insert(ctx, "nat", natChain, 1, "-s", network, "-o", iface, "-m", "comment", "--comment", comment+":masquerade", "-j", "MASQUERADE"); err != nil {
		return err
	}
	return reconciler.ensureJump(ctx, "nat", "POSTROUTING", natChain, comment+":nat")
}

func (reconciler *Reconciler) Cleanup(ctx context.Context, instanceID string) error {
	filterChain, natChain, comment, err := identities(instanceID)
	if err != nil {
		return err
	}
	legacyFilter, legacyNAT, _, err := legacyIdentities(instanceID)
	if err != nil {
		return err
	}
	currentErr := errors.Join(
		reconciler.removeChain(ctx, "nat", "POSTROUTING", natChain, comment+":nat"),
		reconciler.removeChain(ctx, "filter", "FORWARD", filterChain, comment+":forward"),
	)
	if legacyFilter == filterChain && legacyNAT == natChain {
		return currentErr
	}
	return errors.Join(currentErr,
		reconciler.removeChain(ctx, "nat", "POSTROUTING", legacyNAT, comment+":nat"),
		reconciler.removeChain(ctx, "filter", "FORWARD", legacyFilter, comment+":forward"),
	)
}

func identities(instanceID string) (string, string, string, error) {
	if !domain.ValidUUID(instanceID) {
		return "", "", "", fmt.Errorf("invalid network instance UUID")
	}
	digest := sha256.Sum256([]byte(instanceID))
	suffix := hex.EncodeToString(digest[:10])
	return "OVPNF-" + suffix, "OVPNN-" + suffix, "openvpn-docker:" + instanceID, nil
}

func legacyIdentities(instanceID string) (string, string, string, error) {
	if !domain.ValidUUID(instanceID) {
		return "", "", "", fmt.Errorf("invalid network instance UUID")
	}
	compact := strings.ReplaceAll(instanceID, "-", "")[:12]
	return "OVPNF-" + compact, "OVPNN-" + compact, "openvpn-docker:" + instanceID, nil
}

func (reconciler *Reconciler) enableForwarding() error {
	info, err := os.Lstat(reconciler.forwardingFile)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: IPv4 forwarding control is unsafe or unavailable", ErrUnavailable)
	}
	read := func() (string, error) {
		value, err := os.ReadFile(reconciler.forwardingFile)
		return strings.TrimSpace(string(value)), err
	}
	current, err := read()
	if err != nil {
		return fmt.Errorf("%w: read IPv4 forwarding: %v", ErrUnavailable, err)
	}
	if current == "1" {
		return nil
	}
	if current != "0" {
		return fmt.Errorf("IPv4 forwarding control contains unexpected value %q", current)
	}
	file, err := os.OpenFile(reconciler.forwardingFile, os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("%w: enable IPv4 forwarding: %v", ErrUnavailable, err)
	}
	_, writeErr := file.WriteString("1\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		return fmt.Errorf("%w: enable IPv4 forwarding: %v", ErrUnavailable, errors.Join(writeErr, closeErr))
	}
	current, err = read()
	if err != nil || current != "1" {
		return fmt.Errorf("%w: IPv4 forwarding did not become enabled", ErrUnavailable)
	}
	return nil
}

func (reconciler *Reconciler) egressInterface(ctx context.Context, configured string) (string, error) {
	if configured != "auto" {
		if !interfacePattern.MatchString(configured) {
			return "", fmt.Errorf("invalid NAT egress interface")
		}
		return configured, nil
	}
	output, err := reconciler.runner.Run(ctx, reconciler.ipBinary, "-4", "route", "show", "default")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "default" {
			continue
		}
		for index := 1; index+1 < len(fields); index++ {
			if fields[index] == "dev" && interfacePattern.MatchString(fields[index+1]) {
				return fields[index+1], nil
			}
		}
	}
	return "", fmt.Errorf("%w: could not determine the NAT egress interface", ErrUnavailable)
}

func (reconciler *Reconciler) prepareChain(ctx context.Context, table, chain, ownerComment string) error {
	exists, err := reconciler.chainExists(ctx, table, chain)
	if err != nil {
		return err
	}
	if !exists {
		if _, err := reconciler.iptables(ctx, table, "-N", chain); err != nil {
			return err
		}
	} else {
		owned, err := reconciler.ruleExists(ctx, table, chain, ownerRule(ownerComment)...)
		if err != nil {
			return err
		}
		if !owned {
			return fmt.Errorf("refuse to modify unowned network chain %s", chain)
		}
	}
	if _, err = reconciler.iptables(ctx, table, "-F", chain); err != nil {
		return err
	}
	return reconciler.append(ctx, table, chain, ownerRule(ownerComment)...)
}

func (reconciler *Reconciler) append(ctx context.Context, table, chain string, rule ...string) error {
	args := append([]string{"-A", chain}, rule...)
	_, err := reconciler.iptables(ctx, table, args...)
	return err
}

func (reconciler *Reconciler) insert(ctx context.Context, table, chain string, position int, rule ...string) error {
	args := append([]string{"-I", chain, fmt.Sprint(position)}, rule...)
	_, err := reconciler.iptables(ctx, table, args...)
	return err
}

func (reconciler *Reconciler) ensureJump(ctx context.Context, table, parent, chain, comment string) error {
	if err := reconciler.removeJump(ctx, table, parent, chain, comment); err != nil {
		return err
	}
	_, err := reconciler.iptables(ctx, table, "-A", parent, "-m", "comment", "--comment", comment, "-j", chain)
	return err
}

func (reconciler *Reconciler) removeChain(ctx context.Context, table, parent, chain, comment string) error {
	exists, err := reconciler.chainExists(ctx, table, chain)
	if err != nil || !exists {
		return err
	}
	jumpOwned, err := reconciler.ruleExists(ctx, table, parent, "-m", "comment", "--comment", comment, "-j", chain)
	if err != nil {
		return err
	}
	markerComment := strings.TrimSuffix(comment, ":forward") + ":filter-owner"
	if table == "nat" {
		markerComment = strings.TrimSuffix(comment, ":nat") + ":nat-owner"
	}
	markerOwned, err := reconciler.ruleExists(ctx, table, chain, ownerRule(markerComment)...)
	if err != nil {
		return err
	}
	// The exact parent jump recognizes chains created by the immediately prior
	// implementation, while the marker protects chains after that transition.
	if !jumpOwned && !markerOwned {
		return fmt.Errorf("refuse to remove unowned network chain %s", chain)
	}
	if err := reconciler.removeJump(ctx, table, parent, chain, comment); err != nil {
		return err
	}
	if _, err := reconciler.iptables(ctx, table, "-F", chain); err != nil {
		return err
	}
	if _, err := reconciler.iptables(ctx, table, "-X", chain); err != nil {
		return fmt.Errorf("remove instance network chain %s: %w", chain, err)
	}
	return nil
}

func ownerRule(comment string) []string {
	return []string{"-m", "comment", "--comment", comment, "-j", "RETURN"}
}

func (reconciler *Reconciler) ruleExists(ctx context.Context, table, chain string, rule ...string) (bool, error) {
	args := append([]string{"-C", chain}, rule...)
	_, err := reconciler.iptables(ctx, table, args...)
	if err != nil {
		if commandAbsent(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (reconciler *Reconciler) removeJump(ctx context.Context, table, parent, chain, comment string) error {
	rule := []string{"-m", "comment", "--comment", comment, "-j", chain}
	for removed := 0; removed < 64; removed++ {
		check := append([]string{"-C", parent}, rule...)
		if _, err := reconciler.iptables(ctx, table, check...); err != nil {
			if commandAbsent(err) {
				return nil
			}
			return err
		}
		remove := append([]string{"-D", parent}, rule...)
		if _, err := reconciler.iptables(ctx, table, remove...); err != nil {
			return err
		}
	}
	return fmt.Errorf("too many duplicate instance network jumps")
}

func (reconciler *Reconciler) chainExists(ctx context.Context, table, chain string) (bool, error) {
	_, err := reconciler.iptables(ctx, table, "-S", chain)
	if err != nil {
		if commandAbsent(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type exitCoder interface {
	ExitCode() int
}

func commandAbsent(err error) bool {
	var status exitCoder
	return errors.As(err, &status) && status.ExitCode() == 1
}

func (reconciler *Reconciler) iptables(ctx context.Context, table string, args ...string) (string, error) {
	command := []string{"-w", "-t", table}
	command = append(command, args...)
	return reconciler.runner.Run(ctx, reconciler.iptablesBinary, command...)
}
