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
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func runClientList(args []string, stdout, stderr io.Writer) int {
	detail, fullID, jsonMode := false, false, false
	for _, arg := range args {
		if canonicalOption(arg) == "--json" {
			jsonMode = true
		}
	}
	for _, arg := range args {
		switch canonicalOption(arg) {
		case "--detail":
			if detail {
				return writeErrorMode(stderr, usageError("--detail may only be specified once"), jsonMode)
			}
			detail = true
		case "--full-id":
			if fullID {
				return writeErrorMode(stderr, usageError("--full-id may only be specified once"), jsonMode)
			}
			fullID = true
		case "--json":
			if countCanonicalOption(args, "--json") > 1 {
				return writeErrorMode(stderr, usageError("--json may only be specified once"), true)
			}
		default:
			return writeErrorMode(stderr, usageError("usage: ovpn client list [--detail] [--full-id] [--json]"), jsonMode)
		}
	}
	ctx := context.Background()
	service, state, err := openClientService(ctx)
	if err != nil {
		return writeClientError(stderr, err, jsonMode)
	}
	defer state.Close()
	result, err := service.List(ctx)
	if err != nil {
		return writeClientError(stderr, err, jsonMode)
	}
	if detail && len(result.Clients) != 0 {
		populateClientConnections(ctx, &result)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client list", err))
		}
		return int(apperror.ExitSuccess)
	}
	if len(result.Clients) == 0 {
		fmt.Fprintln(stdout, "No clients.")
		return int(apperror.ExitSuccess)
	}
	writer := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if detail {
		fmt.Fprintln(writer, "CLIENT ID\tNAME\tSTATUS\tIPV4 MODE\tIPV4 ADDRESS\tIPV4 STATE\tCONNECTION")
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
			fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", id, value.Name, value.Status, value.IPv4.Mode, address, value.IPv4.State, value.Connection)
		} else {
			fmt.Fprintf(writer, "%s\t%s\t%s\n", id, value.Name, value.Status)
		}
	}
	if err := writer.Flush(); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client list", err))
	}
	return int(apperror.ExitSuccess)
}

func populateClientConnections(ctx context.Context, result *clientservice.ListResult) {
	identities := make(map[string]string, len(result.Clients))
	for index := range result.Clients {
		result.Clients[index].Connection = "unknown"
		identities[result.Clients[index].ID] = result.Clients[index].Name
	}
	status, err := runtimecontrol.QueryStatus(ctx, runtimecontrol.SocketPath(environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)), identities)
	if err != nil {
		return
	}
	online := make(map[string]struct{}, len(status.Clients))
	for _, connected := range status.Clients {
		online[connected.ClientID] = struct{}{}
	}
	for index := range result.Clients {
		result.Clients[index].Connection = "offline"
		if _, ok := online[result.Clients[index].ID]; ok {
			result.Clients[index].Connection = "online"
		}
	}
}

func runClientExport(args []string, stdout, stderr io.Writer) int {
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
	jsonRequested := containsArgument(args, "--json")
	output, args, outputErr := takeOutputOption(args)
	options, args, err := takeClientOutputOptions(args)
	if outputErr != nil || err != nil || len(args) < 1 || len(args) > 3 || strings.HasPrefix(args[0], "-") || (options.JSON && output == "-") {
		return writeErrorMode(stderr, usageError("usage: ovpn client create NAME [--ipv4 [auto|dynamic|ADDRESS]] [--output FILE|-] [--json]"), jsonRequested)
	}
	request := clientservice.CreateRequest{Name: args[0], IPv4: "auto"}
	if len(args) >= 2 {
		if canonicalOption(args[1]) != "--ipv4" {
			return writeErrorMode(stderr, usageError("usage: ovpn client create NAME [--ipv4 [auto|dynamic|ADDRESS]] [--json]"), options.JSON)
		}
		if len(args) == 3 {
			if args[2] == "" || strings.HasPrefix(args[2], "-") {
				return writeErrorMode(stderr, usageError("usage: ovpn client create NAME [--ipv4 [auto|dynamic|ADDRESS]] [--json]"), options.JSON)
			}
			request.IPv4 = args[2]
		}
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.Create(context.Background(), request)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	profileOutput, err := writeCommittedProfile(context.Background(), state, result, output, stdout)
	if err != nil {
		return writeCommittedProfileError(stderr, result, output, err, options.JSON)
	}
	if output == "-" {
		return int(apperror.ExitSuccess)
	}
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientMutationOutput{MutationResult: result, ProfileOutput: profileOutput})
	}
	fmt.Fprintf(stdout, "created client %s [%s] with IPv4 %s\n", result.Client.Name, displayClientID(result.Client.ID, options.FullID), formatIPv4(result.Client.IPv4))
	if profileOutput != nil {
		fmt.Fprintf(stdout, "profile written to %s\n", profileOutput.Destination)
	} else {
		fmt.Fprintf(stdout, "next: ovpn client export --id %s --output FILE\n", clientservice.ShortID(result.Client.ID))
	}
	return int(apperror.ExitSuccess)
}

func runClientRename(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	selector, positionals, err := parseMutationSelector(args)
	if optionErr != nil || err != nil || len(positionals) != 1 {
		return writeErrorMode(stderr, usageError("usage: ovpn client rename (NAME|--name NAME|--id ID) NEW_NAME [--json]"), jsonRequested)
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.Rename(context.Background(), selector, positionals[0])
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, result)
	}
	fmt.Fprintf(stdout, "renamed client to %s [%s]; redistribute the updated profile\n", result.Client.Name, displayClientID(result.Client.ID, options.FullID))
	return int(apperror.ExitSuccess)
}

func runClientRevoke(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	if optionErr != nil {
		return writeErrorMode(stderr, optionErr, jsonRequested)
	}
	release := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		option := canonicalOption(arg)
		if arg == "-4" {
			option = "--release-ipv4"
		}
		if option == "--release-ipv4" {
			if release {
				return writeErrorMode(stderr, usageError("--release-ipv4 may only be specified once"), options.JSON)
			}
			release = true
			continue
		}
		filtered = append(filtered, arg)
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 {
		return writeErrorMode(stderr, usageError("usage: ovpn client revoke (NAME|--name NAME|--id ID) [--release-ipv4] [--json]"), options.JSON)
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.Revoke(context.Background(), selector, release)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	runtimeResult := reconcileClientRuntime(context.Background(), result.Client, result.KickRequired)
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientMutationOutput{MutationResult: result, Runtime: &runtimeResult})
	}
	fmt.Fprintf(stdout, "revoked client %s [%s]\n", result.Client.Name, displayClientID(result.Client.ID, options.FullID))
	writeRuntimeReconcileHuman(stdout, stderr, runtimeResult, options.FullID)
	return int(apperror.ExitSuccess)
}

func runClientReissue(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	output, args, outputErr := takeOutputOption(args)
	options, args, optionErr := takeClientOutputOptions(args)
	if outputErr != nil || optionErr != nil || (options.JSON && output == "-") {
		return writeErrorMode(stderr, usageError("usage: ovpn client reissue (NAME|--name NAME|--id ID) [--ipv4 [auto|dynamic|ADDRESS]] [--output FILE|-] [--json]"), jsonRequested)
	}
	ipv4 := ""
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if canonicalOption(args[index]) != "--ipv4" {
			filtered = append(filtered, args[index])
			continue
		}
		if ipv4 != "" {
			return writeErrorMode(stderr, usageError("usage: ovpn client reissue (NAME|--name NAME|--id ID) [--ipv4 [auto|dynamic|ADDRESS]] [--json]"), options.JSON)
		}
		ipv4, index = optionalIPv4Value(args, index)
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 {
		return writeErrorMode(stderr, usageError("usage: ovpn client reissue (NAME|--name NAME|--id ID) [--ipv4 [auto|dynamic|ADDRESS]] [--json]"), options.JSON)
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.Reissue(context.Background(), selector, ipv4)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	runtimeResult := reconcileClientRuntime(context.Background(), result.Client, result.KickRequired)
	profileOutput, err := writeCommittedProfile(context.Background(), state, result, output, stdout)
	if err != nil {
		return writeCommittedProfileError(stderr, result, output, err, options.JSON)
	}
	if output == "-" {
		writeRuntimeReconcileHuman(io.Discard, stderr, runtimeResult, options.FullID)
		return int(apperror.ExitSuccess)
	}
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientMutationOutput{MutationResult: result, ProfileOutput: profileOutput, Runtime: &runtimeResult})
	}
	fmt.Fprintf(stdout, "reissued client %s [%s] with IPv4 %s; redistribute profile\n", result.Client.Name, displayClientID(result.Client.ID, options.FullID), formatIPv4(result.Client.IPv4))
	if profileOutput != nil {
		fmt.Fprintf(stdout, "profile written to %s\n", profileOutput.Destination)
	}
	writeRuntimeReconcileHuman(stdout, stderr, runtimeResult, options.FullID)
	return int(apperror.ExitSuccess)
}

func runClientDelete(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	if optionErr != nil {
		return writeErrorMode(stderr, optionErr, jsonRequested)
	}
	yes := false
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		if canonicalOption(arg) == "--yes" {
			if yes {
				return writeErrorMode(stderr, usageError("--yes may only be specified once"), options.JSON)
			}
			yes = true
			continue
		}
		filtered = append(filtered, arg)
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 {
		return writeErrorMode(stderr, usageError("usage: ovpn client delete (NAME|--name NAME|--id ID) [--yes] [--json]"), options.JSON)
	}
	service, queryState, err := openClientService(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	_, target, err := service.Select(context.Background(), selector)
	closeErr := queryState.Close()
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	if closeErr != nil {
		return writeClientMutationError(stderr, closeErr, options.JSON)
	}
	if !yes {
		ipv4 := "none"
		if target.Assignment != nil {
			ipv4 = target.Assignment.Kind
			if target.Assignment.Address != nil {
				ipv4 += ":" + target.Assignment.Address.String()
			}
		}
		confirmed, err := confirmAction(stderr, fmt.Sprintf("Type yes to permanently delete client %s [%s] (status %s, IPv4 %s): ", target.Client.Name, displayClientID(target.Client.ID, options.FullID), target.Client.Status, ipv4))
		if err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "client delete requires an interactive confirmation or --yes", err), options.JSON)
		}
		if !confirmed {
			return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "client delete was not confirmed"), options.JSON)
		}
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.Delete(context.Background(), selector)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	runtimeResult := reconcileClientRuntime(context.Background(), result.Client, result.KickRequired)
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientMutationOutput{MutationResult: result, Runtime: &runtimeResult})
	}
	fmt.Fprintf(stdout, "deleted client %s [%s]; UUID tombstone retained\n", result.Client.Name, displayClientID(result.Client.ID, options.FullID))
	writeRuntimeReconcileHuman(stdout, stderr, runtimeResult, options.FullID)
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
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	if optionErr != nil {
		return writeErrorMode(stderr, optionErr, jsonRequested)
	}
	ipv4 := ""
	ipv4Set := false
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if canonicalOption(args[index]) != "--ipv4" {
			filtered = append(filtered, args[index])
			continue
		}
		if ipv4Set {
			return writeErrorMode(stderr, usageError("usage: ovpn client address set (NAME|--name NAME|--id ID) --ipv4 [auto|dynamic|ADDRESS] [--json]"), options.JSON)
		}
		ipv4Set = true
		ipv4, index = optionalIPv4Value(args, index)
	}
	selector, positionals, err := parseMutationSelector(filtered)
	if err != nil || len(positionals) != 0 || !ipv4Set {
		return writeErrorMode(stderr, usageError("usage: ovpn client address set (NAME|--name NAME|--id ID) --ipv4 [auto|dynamic|ADDRESS] [--json]"), options.JSON)
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.AddressSet(context.Background(), selector, ipv4)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	runtimeResults := reconcileAddressRuntime(context.Background(), result)
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientAddressOutput{AddressResult: result, Runtime: runtimeResults})
	}
	fmt.Fprintf(stdout, "updated IPv4 for %s [%s] to %s\n", result.Clients[0].Name, displayClientID(result.Clients[0].ID, options.FullID), formatIPv4(result.Clients[0].IPv4))
	for _, runtimeResult := range runtimeResults {
		writeRuntimeReconcileHuman(stdout, stderr, runtimeResult, options.FullID)
	}
	return int(apperror.ExitSuccess)
}

func runClientAddressRelease(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	selector, positionals, err := parseMutationSelector(args)
	if optionErr != nil || err != nil || len(positionals) != 0 {
		return writeErrorMode(stderr, usageError("usage: ovpn client address release (NAME|--name NAME|--id ID) [--json]"), jsonRequested)
	}
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.AddressRelease(context.Background(), selector)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientAddressOutput{AddressResult: result, Runtime: reconcileAddressRuntime(context.Background(), result)})
	}
	fmt.Fprintf(stdout, "released IPv4 for revoked client %s [%s]\n", result.Clients[0].Name, displayClientID(result.Clients[0].ID, options.FullID))
	return int(apperror.ExitSuccess)
}

func runClientAddressEdit(args []string, stdout, stderr io.Writer) int {
	jsonRequested := containsArgument(args, "--json")
	options, args, optionErr := takeClientOutputOptions(args)
	if optionErr != nil {
		return writeErrorMode(stderr, optionErr, jsonRequested)
	}
	if options.FullID {
		return writeErrorMode(stderr, usageError("--full-id is not applicable to batch address edit output"), options.JSON)
	}
	request := clientservice.AddressEditRequest{}
	positionalNames := make([]string, 0)
	yes, selectorKind := false, ""
	for index := 0; index < len(args); index++ {
		option := canonicalOption(args[index])
		switch option {
		case "--all":
			if request.All {
				return writeErrorMode(stderr, usageError("--all may only be specified once"), options.JSON)
			}
			request.All = true
		case "--yes":
			if yes {
				return writeErrorMode(stderr, usageError("--yes may only be specified once"), options.JSON)
			}
			yes = true
		case "--name", "--id":
			if index+1 >= len(args) {
				return writeErrorMode(stderr, usageError("client selector requires a value"), options.JSON)
			}
			kind := strings.TrimPrefix(option, "--")
			if selectorKind != "" && selectorKind != kind {
				return writeErrorMode(stderr, usageError("--name and --id cannot be mixed"), options.JSON)
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
			if strings.HasPrefix(args[index], "-") {
				return writeErrorMode(stderr, usageError("usage: ovpn client address edit (--all|NAME...|--name NAME...|--id ID...) [--yes] [--json]"), options.JSON)
			}
			positionalNames = append(positionalNames, args[index])
		}
	}
	if len(positionalNames) > 0 {
		if request.All || selectorKind != "" {
			return writeErrorMode(stderr, usageError("positional names cannot be mixed with --all, --name, or --id"), options.JSON)
		}
		for _, name := range positionalNames {
			request.Selectors = append(request.Selectors, clientservice.Selector{Name: name})
		}
	}
	if request.All == (len(request.Selectors) > 0) {
		return writeErrorMode(stderr, usageError("select exactly one of --all, positional names, --name, or --id"), options.JSON)
	}
	service, queryState, err := openClientService(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	targets, err := service.SelectActive(context.Background(), request.All, request.Selectors)
	closeErr := queryState.Close()
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	if closeErr != nil {
		return writeClientMutationError(stderr, closeErr, options.JSON)
	}
	if !yes {
		names := make([]string, 0, len(targets))
		for _, target := range targets {
			names = append(names, target.Name)
		}
		confirmed, err := confirmAction(stderr, fmt.Sprintf("Type yes to edit IPv4 assignments for %d client(s) (%s): ", len(targets), strings.Join(names, ", ")))
		if err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "batch address edit requires an interactive confirmation or --yes", err), options.JSON)
		}
		if !confirmed {
			return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "batch address edit was not confirmed"), options.JSON)
		}
	}
	request.Edit = runAddressEditor
	manager, state, err := openClientManager(context.Background())
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	defer state.Close()
	result, err := manager.AddressEdit(context.Background(), request)
	if err != nil {
		return writeClientMutationError(stderr, err, options.JSON)
	}
	runtimeResults := reconcileAddressRuntime(context.Background(), result)
	if options.JSON {
		return writeClientMutationJSON(stdout, stderr, clientAddressOutput{AddressResult: result, Runtime: runtimeResults})
	}
	fmt.Fprintf(stdout, "updated IPv4 assignments for %d clients\n", len(result.Clients))
	for _, runtimeResult := range runtimeResults {
		writeRuntimeReconcileHuman(stdout, stderr, runtimeResult, false)
	}
	return int(apperror.ExitSuccess)
}

func runAddressEditor(path string) error {
	editor, err := selectAddressEditor()
	if err != nil {
		return err
	}
	resolved, err := exec.LookPath(editor)
	if err != nil {
		return fmt.Errorf("%w: editor %s", pki.ErrUnavailable, editor)
	}
	command := exec.Command(resolved, path)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		defer terminal.Close()
		command.Stdin = terminal
		command.Stdout = terminal
		command.Stderr = terminal
	}
	return command.Run()
}

func selectAddressEditor() (string, error) {
	editor := os.Getenv("OVPN_EDITOR")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "nano"
	}
	if strings.ContainsAny(editor, " \t\r\n") {
		return "", fmt.Errorf("%w: editor must be a single executable path", clientservice.ErrInvalidRequest)
	}
	return editor, nil
}

func parseClientExport(args []string) (clientservice.Selector, string, error) {
	var selector clientservice.Selector
	var output string
	positionals := make([]string, 0, 1)
	for index := 0; index < len(args); index++ {
		option := canonicalOption(args[index])
		switch option {
		case "-h", "--help":
			return clientservice.Selector{}, "", usageError("usage: ovpn client export (NAME|--name NAME|--id ID) [--output FILE|-]")
		case "--name", "--id", "--output":
			if index+1 >= len(args) {
				return clientservice.Selector{}, "", usageError("usage: ovpn client export (NAME|--name NAME|--id ID) [--output FILE|-]")
			}
			value := args[index+1]
			index++
			switch option {
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
			if strings.HasPrefix(args[index], "-") {
				return clientservice.Selector{}, "", usageError("usage: ovpn client export (NAME|--name NAME|--id ID) [--output FILE|-]")
			}
			positionals = append(positionals, args[index])
		}
	}
	if selector.Name != "" && selector.IDPrefix != "" {
		return clientservice.Selector{}, "", usageError("exactly one of positional NAME, --name, or --id is required")
	}
	if selector.Name == "" && selector.IDPrefix == "" {
		if len(positionals) != 1 {
			return clientservice.Selector{}, "", usageError("exactly one of positional NAME, --name, or --id is required")
		}
		selector.Name = positionals[0]
		positionals = nil
	}
	if len(positionals) > 0 {
		return clientservice.Selector{}, "", usageError("positional name cannot be mixed with --name or --id")
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
		option := canonicalOption(args[index])
		switch option {
		case "--name", "--id":
			if index+1 >= len(args) {
				return clientservice.Selector{}, nil, usageError("client selector requires a value")
			}
			value := args[index+1]
			index++
			if option == "--name" {
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
	if selector.Name != "" && selector.IDPrefix != "" {
		return clientservice.Selector{}, nil, usageError("exactly one of positional NAME, --name, or --id is required")
	}
	if selector.Name == "" && selector.IDPrefix == "" {
		if len(positionals) == 0 {
			return clientservice.Selector{}, nil, usageError("exactly one of positional NAME, --name, or --id is required")
		}
		selector.Name = positionals[0]
		positionals = positionals[1:]
	}
	return selector, positionals, nil
}

func optionalIPv4Value(args []string, optionIndex int) (string, int) {
	if optionIndex+1 < len(args) && args[optionIndex+1] != "" && !strings.HasPrefix(args[optionIndex+1], "-") {
		return args[optionIndex+1], optionIndex + 1
	}
	return "auto", optionIndex
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

func writeClientMutationError(stderr io.Writer, err error, jsonMode bool) int {
	var applicationError *apperror.Error
	if errors.As(err, &applicationError) {
		return writeErrorMode(stderr, err, jsonMode)
	}
	switch {
	case errors.Is(err, artifact.ErrLocked):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "lock_conflict", "client mutation lock is unavailable", err), jsonMode)
	case errors.Is(err, pki.ErrUnavailable):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitUnavailable, "dependency_unavailable", "client mutation dependency is unavailable", err), jsonMode)
	case errors.Is(err, clientservice.ErrInvalidRequest), errors.Is(err, clientservice.ErrConflict), errors.Is(err, clientservice.ErrNotFound), errors.Is(err, clientservice.ErrAmbiguous), errors.Is(err, storesqlite.ErrConstraint):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitData, "client_request", err.Error(), err), jsonMode)
	case errors.Is(err, clientservice.ErrArtifactMismatch), errors.Is(err, pki.ErrInvalidMaterial), errors.Is(err, artifact.ErrUnsafePath), errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrUnsupportedSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision), errors.Is(err, storesqlite.ErrMissing), errors.Is(err, storesqlite.ErrPermission):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "client_state_refused", err.Error(), err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "client_mutation_failed", "client mutation failed", err), jsonMode)
	}
}

func usageError(message string) error {
	return apperror.New(apperror.ExitUsage, "usage", message)
}

type clientOutputOptions struct {
	JSON   bool
	FullID bool
}

type clientProfileOutput struct {
	Destination string `json:"destination"`
	Written     bool   `json:"written"`
}

type clientMutationOutput struct {
	clientservice.MutationResult
	ProfileOutput *clientProfileOutput `json:"profile_output,omitempty"`
	Runtime       *clientRuntimeResult `json:"runtime,omitempty"`
}

type clientAddressOutput struct {
	clientservice.AddressResult
	Runtime []clientRuntimeResult `json:"runtime"`
}

type clientRuntimeResult struct {
	ClientID    string `json:"client_id"`
	ClientName  string `json:"client_name"`
	Status      string `json:"status"`
	Connections int    `json:"connections"`
	Warning     string `json:"warning,omitempty"`
}

func takeClientOutputOptions(args []string) (clientOutputOptions, []string, error) {
	options := clientOutputOptions{}
	filtered := make([]string, 0, len(args))
	for _, arg := range args {
		switch canonicalOption(arg) {
		case "--json":
			if options.JSON {
				return options, nil, usageError("--json may only be specified once")
			}
			options.JSON = true
		case "--full-id":
			if options.FullID {
				return options, nil, usageError("--full-id may only be specified once")
			}
			options.FullID = true
		default:
			filtered = append(filtered, arg)
		}
	}
	return options, filtered, nil
}

func takeOutputOption(args []string) (string, []string, error) {
	output := ""
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if canonicalOption(args[index]) != "--output" {
			filtered = append(filtered, args[index])
			continue
		}
		if output != "" || index+1 >= len(args) {
			return "", nil, usageError("--output requires exactly one destination")
		}
		value := args[index+1]
		if value == "" || (strings.HasPrefix(value, "-") && value != "-") {
			return "", nil, usageError("--output requires FILE or -")
		}
		output = value
		index++
	}
	return output, filtered, nil
}

func writeCommittedProfile(ctx context.Context, state *storesqlite.Store, result clientservice.MutationResult, output string, stdout io.Writer) (*clientProfileOutput, error) {
	if output == "" {
		return nil, nil
	}
	local, err := artifact.NewLocal(environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir))
	if err != nil {
		return nil, err
	}
	service, err := clientservice.NewService(state, local)
	if err != nil {
		return nil, err
	}
	content, _, err := service.Export(ctx, clientservice.Selector{IDPrefix: result.Client.ID})
	if err != nil {
		return nil, err
	}
	return writeProfileDestination(output, content, stdout)
}

func writeProfileDestination(output string, content []byte, stdout io.Writer) (*clientProfileOutput, error) {
	destination := output
	if output == "-" {
		destination = "stdout"
		if _, err := stdout.Write(content); err != nil {
			return nil, err
		}
	} else if err := writeExportFile(output, content); err != nil {
		return nil, err
	}
	return &clientProfileOutput{Destination: destination, Written: true}, nil
}

func writeCommittedProfileError(stderr io.Writer, result clientservice.MutationResult, output string, cause error, jsonMode bool) int {
	destination := output
	if output == "-" {
		destination = "stdout"
	}
	err := apperror.Wrap(apperror.ExitFailure, "profile_output_failed", fmt.Sprintf("client %s [%s] was committed, but profile output to %s failed", result.Client.Name, clientservice.ShortID(result.Client.ID), destination), cause)
	errWithHint := apperror.WithHint(err, fmt.Sprintf("rerun 'ovpn client export --id %s --output FILE' to retrieve the committed profile", clientservice.ShortID(result.Client.ID)))
	return writeErrorMode(stderr, errWithHint, jsonMode)
}

func reconcileClientRuntime(ctx context.Context, client clientservice.View, required bool) clientRuntimeResult {
	result := clientRuntimeResult{ClientID: client.ID, ClientName: client.Name, Status: "not_required"}
	if !required {
		return result
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	disconnect, err := runtimecontrol.Disconnect(ctx, runtimecontrol.SocketPath(runtimeDir), client.ID, client.Name)
	if err != nil {
		result.Status = "pending"
		result.Warning = "OpenVPN runtime is unavailable; retry with 'ovpn runtime disconnect --id " + clientservice.ShortID(client.ID) + "' in the active server container"
		return result
	}
	result.Connections = disconnect.Connections
	if disconnect.Disconnected {
		result.Status = "disconnected"
	} else {
		result.Status = "already_offline"
	}
	return result
}

func reconcileAddressRuntime(ctx context.Context, result clientservice.AddressResult) []clientRuntimeResult {
	required := make(map[string]bool, len(result.KickRequired))
	for _, clientID := range result.KickRequired {
		required[clientID] = true
	}
	values := make([]clientRuntimeResult, 0, len(result.Clients))
	runtimeUnavailable := false
	for _, client := range result.Clients {
		if runtimeUnavailable && required[client.ID] {
			values = append(values, clientRuntimeResult{ClientID: client.ID, ClientName: client.Name, Status: "pending", Warning: "OpenVPN runtime is unavailable; retry with 'ovpn runtime disconnect --id " + clientservice.ShortID(client.ID) + "' in the active server container"})
			continue
		}
		value := reconcileClientRuntime(ctx, client, required[client.ID])
		if value.Status == "pending" {
			runtimeUnavailable = true
		}
		values = append(values, value)
	}
	return values
}

func writeRuntimeReconcileHuman(stdout, stderr io.Writer, result clientRuntimeResult, fullID bool) {
	identity := fmt.Sprintf("%s [%s]", result.ClientName, displayClientID(result.ClientID, fullID))
	switch result.Status {
	case "disconnected":
		fmt.Fprintf(stdout, "runtime: disconnected %s (%d connection(s))\n", identity, result.Connections)
	case "already_offline":
		fmt.Fprintf(stdout, "runtime: %s was already offline\n", identity)
	case "pending":
		fmt.Fprintf(stderr, "ovpn: warning: %s\n", result.Warning)
	}
}

func writeClientMutationJSON(stdout, stderr io.Writer, result any) int {
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write client mutation result", err), true)
	}
	return int(apperror.ExitSuccess)
}

func displayClientID(id string, full bool) string {
	if full {
		return id
	}
	return clientservice.ShortID(id)
}
