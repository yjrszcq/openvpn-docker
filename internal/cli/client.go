package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func runClientList(args []string, stdout, stderr io.Writer) int {
	detail, fullID, jsonMode := false, false, false
	for _, arg := range args {
		if arg == "--json" {
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch arg {
		case "--detail":
			detail = true
		case "--full-id":
			fullID = true
		case "--json":
		case "-h", "--help":
			if len(args) != 1 {
				return writeErrorMode(stderr, usageError("usage: ovpn client list [--detail] [--full-id] [--json]"), jsonMode)
			}
			fmt.Fprintln(stdout, "Usage: ovpn client list [--detail] [--full-id] [--json]")
			return int(apperror.ExitSuccess)
		default:
			return writeErrorMode(stderr, usageError("usage: ovpn client list [--detail] [--full-id] [--json]"), jsonMode)
		}
	}
	service, state, err := openClientService(context.Background())
	if err != nil {
		return writeClientError(stderr, err, jsonMode)
	}
	defer state.Close()
	result, err := service.List(context.Background())
	if err != nil {
		return writeClientError(stderr, err, jsonMode)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client list", err))
		}
		return int(apperror.ExitSuccess)
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if detail {
		fmt.Fprintln(writer, "CLIENT ID\tNAME\tSTATUS\tIPV4 MODE\tIPV4 ADDRESS\tIPV4 STATE")
	} else {
		fmt.Fprintln(writer, "CLIENT ID\tNAME\tSTATUS")
	}
	for _, value := range result.Clients {
		id := value.ID
		if !fullID {
			id = clientservice.ShortID(id)
		}
		if detail {
			address := "-"
			if value.IPv4.Address != nil {
				address = *value.IPv4.Address
			}
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\n", id, value.Name, value.Status, value.IPv4.Mode, address, value.IPv4.State)
		} else {
			fmt.Fprintf(writer, "%s\t%s\t%s\n", id, value.Name, value.Status)
		}
	}
	if err := writer.Flush(); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client list", err))
	}
	return int(apperror.ExitSuccess)
}

func runClientExport(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client export (--name NAME|--id ID) [--output FILE|-]")
		return int(apperror.ExitSuccess)
	}
	selector, output, err := parseClientExport(args)
	if err != nil {
		return writeError(stderr, err)
	}
	service, state, err := openClientService(context.Background())
	if err != nil {
		return writeClientError(stderr, err, false)
	}
	defer state.Close()
	content, _, err := service.Export(context.Background(), selector)
	if err != nil {
		return writeClientError(stderr, err, false)
	}
	if output == "" || output == "-" {
		if _, err := stdout.Write(content); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client profile", err))
		}
		return int(apperror.ExitSuccess)
	}
	if err := writeExportFile(output, content); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client profile", err))
	}
	return int(apperror.ExitSuccess)
}

func runClientCreate(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client create NAME [--ipv4 auto|dynamic|ADDRESS]")
		return int(apperror.ExitSuccess)
	}
	if len(args) < 1 || len(args) > 3 || len(args) == 2 || strings.HasPrefix(args[0], "-") {
		return writeError(stderr, usageError("usage: ovpn client create NAME [--ipv4 auto|dynamic|ADDRESS]"))
	}
	request := clientservice.CreateRequest{Name: args[0], IPv4: "auto"}
	if len(args) == 3 {
		if args[1] != "--ipv4" || args[2] == "" {
			return writeError(stderr, usageError("usage: ovpn client create NAME [--ipv4 auto|dynamic|ADDRESS]"))
		}
		request.IPv4 = args[2]
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.Create(context.Background(), request)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "created client %s [%s] with IPv4 %s\n", result.Client.Name, result.Client.ID, formatIPv4(result.Client.IPv4))
	return int(apperror.ExitSuccess)
}

func runClientRename(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client rename (--name NAME|--id ID) NEW_NAME")
		return int(apperror.ExitSuccess)
	}
	selector, positionals, err := parseMutationSelector(args)
	if err != nil || len(positionals) != 1 {
		return writeError(stderr, usageError("usage: ovpn client rename (--name NAME|--id ID) NEW_NAME"))
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.Rename(context.Background(), selector, positionals[0])
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "renamed client to %s [%s]\n", result.Client.Name, result.Client.ID)
	return int(apperror.ExitSuccess)
}

func runClientRevoke(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client revoke (--name NAME|--id ID) [--release-ipv4]")
		return int(apperror.ExitSuccess)
	}
	release := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--release-ipv4" {
			if release {
				return writeError(stderr, usageError("--release-ipv4 may only be specified once"))
			}
			release = true
			continue
		}
		filtered = append(filtered, arg)
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 {
		return writeError(stderr, usageError("usage: ovpn client revoke (--name NAME|--id ID) [--release-ipv4]"))
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.Revoke(context.Background(), selector, release)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "revoked client %s [%s]; runtime disconnect required\n", result.Client.Name, result.Client.ID)
	return int(apperror.ExitSuccess)
}

func runClientReissue(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client reissue (--name NAME|--id ID) [--ipv4 auto|dynamic|ADDRESS]")
		return int(apperror.ExitSuccess)
	}
	ipv4 := ""
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] != "--ipv4" {
			filtered = append(filtered, args[index])
			continue
		}
		if ipv4 != "" || index+1 >= len(args) || args[index+1] == "" {
			return writeError(stderr, usageError("usage: ovpn client reissue (--name NAME|--id ID) [--ipv4 auto|dynamic|ADDRESS]"))
		}
		ipv4 = args[index+1]
		index++
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 {
		return writeError(stderr, usageError("usage: ovpn client reissue (--name NAME|--id ID) [--ipv4 auto|dynamic|ADDRESS]"))
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.Reissue(context.Background(), selector, ipv4)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "reissued client %s [%s] with IPv4 %s; redistribute profile and disconnect prior session\n", result.Client.Name, result.Client.ID, formatIPv4(result.Client.IPv4))
	return int(apperror.ExitSuccess)
}

func runClientDelete(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client delete (--name NAME|--id ID) [--yes]")
		return int(apperror.ExitSuccess)
	}
	yes := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "--yes" {
			if yes {
				return writeError(stderr, usageError("--yes may only be specified once"))
			}
			yes = true
			continue
		}
		filtered = append(filtered, arg)
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 {
		return writeError(stderr, usageError("usage: ovpn client delete (--name NAME|--id ID) --yes"))
	}
	if !yes {
		confirmed, err := confirmAction(stderr, "Type yes to permanently delete the client credentials: ")
		if err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "client delete requires an interactive confirmation or --yes", err))
		}
		if !confirmed {
			return writeError(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "client delete was not confirmed"))
		}
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.Delete(context.Background(), selector)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "deleted client %s [%s]; UUID tombstone retained\n", result.Client.Name, result.Client.ID)
	return int(apperror.ExitSuccess)
}

func confirmAction(stderr io.Writer, prompt string) (bool, error) {
	if !isTerminal(os.Stdin) {
		return false, fmt.Errorf("stdin is not a TTY")
	}
	fmt.Fprint(stderr, prompt)
	value, err := bufio.NewReader(io.LimitReader(os.Stdin, 16)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, err
	}
	return strings.TrimSpace(value) == "yes", nil
}

func runClientAddressSet(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client address set (--name NAME|--id ID) --ipv4 auto|dynamic|ADDRESS")
		return int(apperror.ExitSuccess)
	}
	ipv4 := ""
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] != "--ipv4" {
			filtered = append(filtered, args[index])
			continue
		}
		if ipv4 != "" || index+1 >= len(args) || args[index+1] == "" {
			return writeError(stderr, usageError("usage: ovpn client address set (--name NAME|--id ID) --ipv4 auto|dynamic|ADDRESS"))
		}
		ipv4 = args[index+1]
		index++
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 || ipv4 == "" {
		return writeError(stderr, usageError("usage: ovpn client address set (--name NAME|--id ID) --ipv4 auto|dynamic|ADDRESS"))
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.AddressSet(context.Background(), selector, ipv4)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "updated IPv4 for %s [%s] to %s\n", result.Clients[0].Name, result.Clients[0].ID, formatIPv4(result.Clients[0].IPv4))
	return int(apperror.ExitSuccess)
}

func runClientAddressRelease(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client address release (--name NAME|--id ID)")
		return int(apperror.ExitSuccess)
	}
	selector, positionals, err := parseMutationSelector(args)
	if err != nil || len(positionals) != 0 {
		return writeError(stderr, usageError("usage: ovpn client address release (--name NAME|--id ID)"))
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.AddressRelease(context.Background(), selector)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "released IPv4 for revoked client %s [%s]\n", result.Clients[0].Name, result.Clients[0].ID)
	return int(apperror.ExitSuccess)
}

func runClientAddressEdit(args []string, stdout, stderr io.Writer) int {
	if len(args) == 1 && isHelp(args[0]) {
		fmt.Fprintln(stdout, "Usage: ovpn client address edit (--all|--name NAME...|--id ID...) [--yes]")
		return int(apperror.ExitSuccess)
	}
	request := clientservice.AddressEditRequest{}
	yes, selectorKind := false, ""
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--all":
			if request.All {
				return writeError(stderr, usageError("--all may only be specified once"))
			}
			request.All = true
		case "--yes":
			if yes {
				return writeError(stderr, usageError("--yes may only be specified once"))
			}
			yes = true
		case "--name", "--id":
			if index+1 >= len(args) {
				return writeError(stderr, usageError("client selector requires a value"))
			}
			kind := strings.TrimPrefix(args[index], "--")
			if selectorKind != "" && selectorKind != kind {
				return writeError(stderr, usageError("--name and --id cannot be mixed"))
			}
			selectorKind = kind
			index++
			selector := clientservice.Selector{}
			if kind == "name" {
				selector.Name = args[index]
			} else {
				selector.IDPrefix = args[index]
			}
			request.Selectors = append(request.Selectors, selector)
		default:
			return writeError(stderr, usageError("usage: ovpn client address edit (--all|--name NAME...|--id ID...) [--yes]"))
		}
	}
	if request.All == (len(request.Selectors) > 0) {
		return writeError(stderr, usageError("select exactly one of --all, --name, or --id"))
	}
	if !yes {
		confirmed, err := confirmAction(stderr, "Type yes to edit multiple client IPv4 assignments: ")
		if err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "batch address edit requires an interactive confirmation or --yes", err))
		}
		if !confirmed {
			return writeError(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "batch address edit was not confirmed"))
		}
	}
	request.Edit = runAddressEditor
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	defer state.Close()
	result, err := manager.AddressEdit(context.Background(), request)
	if err != nil {
		return writeClientMutationError(stderr, err)
	}
	fmt.Fprintf(stdout, "updated IPv4 assignments for %d clients; runtime disconnect required for each\n", len(result.Clients))
	return int(apperror.ExitSuccess)
}

func runAddressEditor(path string) error {
	editor := os.Getenv("OVPN_EDITOR")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	if strings.ContainsAny(editor, " \t\r\n") {
		return fmt.Errorf("%w: editor must be a single executable path", clientservice.ErrInvalidRequest)
	}
	resolved, err := exec.LookPath(editor)
	if err != nil {
		return fmt.Errorf("%w: editor %s", pki.ErrUnavailable, editor)
	}
	command := exec.Command(resolved, path)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	return command.Run()
}

func parseClientExport(args []string) (clientservice.Selector, string, error) {
	var selector clientservice.Selector
	var output string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "-h", "--help":
			return clientservice.Selector{}, "", usageError("usage: ovpn client export (--name NAME|--id ID) [--output FILE|-]")
		case "--name", "--id", "--output":
			if index+1 >= len(args) {
				return clientservice.Selector{}, "", usageError("usage: ovpn client export (--name NAME|--id ID) [--output FILE|-]")
			}
			value := args[index+1]
			index++
			switch args[index-1] {
			case "--name":
				if selector.Name != "" {
					return clientservice.Selector{}, "", usageError("--name may only be specified once")
				}
				selector.Name = value
			case "--id":
				if selector.IDPrefix != "" {
					return clientservice.Selector{}, "", usageError("--id may only be specified once")
				}
				selector.IDPrefix = value
			case "--output":
				if output != "" {
					return clientservice.Selector{}, "", usageError("--output may only be specified once")
				}
				output = value
			}
		default:
			return clientservice.Selector{}, "", usageError("usage: ovpn client export (--name NAME|--id ID) [--output FILE|-]")
		}
	}
	if (selector.Name == "") == (selector.IDPrefix == "") {
		return clientservice.Selector{}, "", usageError("exactly one of --name or --id is required")
	}
	return selector, output, nil
}

func openClientService(ctx context.Context) (*clientservice.Service, *storesqlite.Store, error) {
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	state, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return nil, nil, err
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		_ = state.Close()
		return nil, nil, err
	}
	service, err := clientservice.NewService(state, local)
	if err != nil {
		_ = state.Close()
		return nil, nil, err
	}
	return service, state, nil
}

func openClientManager(ctx context.Context) (*clientservice.Manager, *storesqlite.Store, error) {
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	state, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return nil, nil, err
	}
	closeError := func(err error) (*clientservice.Manager, *storesqlite.Store, error) {
		_ = state.Close()
		return nil, nil, err
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		return closeError(err)
	}
	contract, err := compatibility.Load(environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath))
	if err != nil {
		return closeError(apperror.Wrap(apperror.ExitPolicy, "invalid_compatibility_contract", "compatibility contract is invalid", err))
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return closeError(apperror.Wrap(apperror.ExitPolicy, "invalid_templates", "OpenVPN templates are invalid", err))
	}
	runner, err := runtimePKIRunner()
	if err != nil {
		return closeError(err)
	}
	manager, err := clientservice.NewManager(state, local, runner, renderer, render.Paths{DataDir: dataDir, RuntimeDir: environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)})
	if err != nil {
		return closeError(err)
	}
	return manager, state, nil
}

func runtimePKIRunner() (*pki.Runner, error) {
	easyRSA := os.Getenv("OVPN_EASYRSA_BIN")
	if easyRSA == "" {
		if info, err := os.Stat("/usr/share/easy-rsa/easyrsa"); err == nil && info.Mode().IsRegular() {
			easyRSA = "/usr/share/easy-rsa/easyrsa"
		} else {
			easyRSA = "easyrsa"
		}
	}
	return pki.NewRunner(pki.Config{EasyRSABinary: easyRSA, OpenVPNBinary: environmentOr("OVPN_OPENVPN_BIN", "openvpn")}, nil)
}

func parseMutationSelector(args []string) (clientservice.Selector, []string, error) {
	var selector clientservice.Selector
	positionals := make([]string, 0, 1)
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--name", "--id":
			if index+1 >= len(args) {
				return clientservice.Selector{}, nil, usageError("client selector requires a value")
			}
			value := args[index+1]
			index++
			if args[index-1] == "--name" {
				if selector.Name != "" {
					return clientservice.Selector{}, nil, usageError("--name may only be specified once")
				}
				selector.Name = value
			} else {
				if selector.IDPrefix != "" {
					return clientservice.Selector{}, nil, usageError("--id may only be specified once")
				}
				selector.IDPrefix = value
			}
		default:
			if strings.HasPrefix(args[index], "-") {
				return clientservice.Selector{}, nil, usageError("unknown client option")
			}
			positionals = append(positionals, args[index])
		}
	}
	if (selector.Name == "") == (selector.IDPrefix == "") {
		return clientservice.Selector{}, nil, usageError("exactly one of --name or --id is required")
	}
	return selector, positionals, nil
}

func formatIPv4(value clientservice.IPv4View) string {
	if value.Address != nil {
		return value.Mode + ":" + *value.Address
	}
	return value.Mode
}

func writeExportFile(path string, content []byte) (resultErr error) {
	if path == "" || filepath.Clean(path) != path || filepath.Base(path) == "." || filepath.Base(path) == ".." {
		return fmt.Errorf("output path must be clean")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	created := true
	defer func() {
		if resultErr != nil && created {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	syncErr := parent.Sync()
	closeErr := parent.Close()
	if syncErr != nil || closeErr != nil {
		return errors.Join(syncErr, closeErr)
	}
	created = false
	return nil
}

func writeClientError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, clientservice.ErrNotFound), errors.Is(err, clientservice.ErrAmbiguous):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitData, "client_selector", err.Error(), err), jsonMode)
	case errors.Is(err, clientservice.ErrInactive), errors.Is(err, clientservice.ErrArtifactMismatch), errors.Is(err, artifact.ErrUnsafePath), errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrUnsupportedSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision), errors.Is(err, storesqlite.ErrMissing), errors.Is(err, storesqlite.ErrPermission):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "client_state_refused", err.Error(), err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "client_query_failed", "client query failed", err), jsonMode)
	}
}

func writeClientMutationError(stderr io.Writer, err error) int {
	var applicationError *apperror.Error
	if errors.As(err, &applicationError) {
		return writeError(stderr, err)
	}
	switch {
	case errors.Is(err, artifact.ErrLocked):
		return writeError(stderr, apperror.Wrap(apperror.ExitTemporary, "lock_conflict", "client mutation lock is unavailable", err))
	case errors.Is(err, pki.ErrUnavailable):
		return writeError(stderr, apperror.Wrap(apperror.ExitUnavailable, "dependency_unavailable", "client mutation dependency is unavailable", err))
	case errors.Is(err, clientservice.ErrInvalidRequest), errors.Is(err, clientservice.ErrConflict), errors.Is(err, clientservice.ErrNotFound), errors.Is(err, clientservice.ErrAmbiguous), errors.Is(err, storesqlite.ErrConstraint):
		return writeError(stderr, apperror.Wrap(apperror.ExitData, "client_request", err.Error(), err))
	case errors.Is(err, clientservice.ErrArtifactMismatch), errors.Is(err, pki.ErrInvalidMaterial), errors.Is(err, artifact.ErrUnsafePath), errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrUnsupportedSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision), errors.Is(err, storesqlite.ErrMissing), errors.Is(err, storesqlite.ErrPermission):
		return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "client_state_refused", err.Error(), err))
	default:
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "client_mutation_failed", "client mutation failed", err))
	}
}

func usageError(message string) error {
	return apperror.New(apperror.ExitUsage, "usage", message)
}
