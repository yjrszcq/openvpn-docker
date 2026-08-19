package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/auditactor"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func runAPIKeyCreate(args []string, stdout, stderr io.Writer) int {
	jsonMode := countCanonicalOption(args, "--json") > 0
	if countCanonicalOption(args, "--json") > 1 {
		return writeErrorMode(stderr, usageError("--json may only be specified once"), true)
	}
	output, filtered, err := takeOutputOption(args)
	if err != nil {
		return writeErrorMode(stderr, err, jsonMode)
	}
	positionals := make([]string, 0, 1)
	for _, arg := range filtered {
		if canonicalOption(arg) == "--json" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return writeErrorMode(stderr, usageError("usage: ovpn api key create NAME [--output FILE|-] [--json]"), jsonMode)
		}
		positionals = append(positionals, arg)
	}
	if len(positionals) != 1 || (jsonMode && output == "-") {
		return writeErrorMode(stderr, usageError("usage: ovpn api key create NAME [--output FILE|-] [--json]"), jsonMode)
	}
	name := positionals[0]
	service, store, err := openAPIKeyService(auditactor.LocalCLI(context.Background()))
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode)
	}
	defer store.Close()
	result, err := service.Create(auditactor.LocalCLI(context.Background()), name)
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode)
	}
	if output == "-" {
		fmt.Fprintln(stdout, result.Secret)
		return int(apperror.ExitSuccess)
	}
	if output != "" {
		if err := writeExportFile(output, []byte(result.Secret+"\n")); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "API key was created but its secret could not be written; delete the key and create another", err), jsonMode)
		}
		result.Secret = ""
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write API key result", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "created API key %s [%s]\n", result.Key.Name, result.Key.ID)
	if output == "" {
		fmt.Fprintf(stdout, "%s\n", result.Secret)
	} else {
		fmt.Fprintf(stdout, "secret written to %s\n", output)
	}
	return int(apperror.ExitSuccess)
}

func runAPIKeyList(args []string, stdout, stderr io.Writer) int {
	jsonMode, ok := parseJSONOnly(args, stdout, stderr, "ovpn api key list")
	if !ok {
		return jsonMode
	}
	service, store, err := openAPIKeyService(context.Background())
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode == 1)
	}
	defer store.Close()
	result, err := service.List(context.Background())
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode == 1)
	}
	if jsonMode == 1 {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write API key list", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintln(stdout, "ID                                    NAME                 CREATED")
	for _, key := range result.Keys {
		fmt.Fprintf(stdout, "%-36s  %-20s %s\n", key.ID, key.Name, key.CreatedAt.Format("2006-01-02T15:04:05Z"))
	}
	return int(apperror.ExitSuccess)
}

func runAPIKeyDelete(args []string, stdout, stderr io.Writer) int {
	jsonMode := containsArgument(args, "--json") || containsArgument(args, "-j")
	yes := false
	selector := apikey.Selector{}
	positionals := make([]string, 0, 1)
	for index := 0; index < len(args); index++ {
		switch canonicalOption(args[index]) {
		case "--json":
			if countCanonicalOption(args, "--json") > 1 {
				return writeErrorMode(stderr, usageError("--json may only be specified once"), true)
			}
			jsonMode = true
		case "--yes":
			if yes {
				return writeErrorMode(stderr, usageError("--yes may only be specified once"), jsonMode)
			}
			yes = true
		case "--name", "--id":
			option := canonicalOption(args[index])
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
				return writeErrorMode(stderr, usageError(option+" requires a value"), jsonMode)
			}
			index++
			if option == "--name" {
				if selector.Name != "" {
					return writeErrorMode(stderr, usageError("--name may only be specified once"), jsonMode)
				}
				selector.Name = args[index]
			} else {
				if selector.IDPrefix != "" {
					return writeErrorMode(stderr, usageError("--id may only be specified once"), jsonMode)
				}
				selector.IDPrefix = args[index]
			}
		default:
			if strings.HasPrefix(args[index], "-") {
				return writeErrorMode(stderr, usageError("unknown API key option"), jsonMode)
			}
			positionals = append(positionals, args[index])
		}
	}
	if selector.Name != "" && selector.IDPrefix != "" || len(positionals) > 1 || (len(positionals) == 1 && (selector.Name != "" || selector.IDPrefix != "")) {
		return writeErrorMode(stderr, usageError("exactly one of positional NAME, --name, or --id is required"), jsonMode)
	}
	if len(positionals) == 1 {
		selector.Name = positionals[0]
	}
	if selector.Name == "" && selector.IDPrefix == "" {
		return writeErrorMode(stderr, usageError("exactly one of positional NAME, --name, or --id is required"), jsonMode)
	}
	ctx := auditactor.LocalCLI(context.Background())
	service, store, err := openAPIKeyService(ctx)
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode)
	}
	defer store.Close()
	key, err := service.Select(ctx, selector)
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode)
	}
	if !yes {
		confirmed, err := confirmAction(stderr, fmt.Sprintf("Type yes to delete API key %s [%s]: ", key.Name, key.ID))
		if err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "API key deletion requires an interactive confirmation or --yes", err), jsonMode)
		}
		if !confirmed {
			return writeErrorMode(stderr, apperror.New(apperror.ExitPolicy, "confirmation_required", "API key deletion was not confirmed"), jsonMode)
		}
	}
	result, err := service.Delete(ctx, selector)
	if err != nil {
		return writeAPIKeyError(stderr, err, jsonMode)
	}
	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "write API key deletion", err), true)
		}
		return int(apperror.ExitSuccess)
	}
	fmt.Fprintf(stdout, "deleted API key %s [%s]\n", result.Key.Name, result.Key.ID)
	return int(apperror.ExitSuccess)
}

func openAPIKeyService(ctx context.Context) (*apikey.Service, *storesqlite.Store, error) {
	path := filepath.Join(environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir), "meta", "state.db")
	store, err := storesqlite.Open(ctx, path)
	if err != nil {
		return nil, nil, err
	}
	instance, err := store.LoadOnlyInstance(ctx)
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	service, err := apikey.NewService(store, instance.ID)
	if err != nil {
		_ = store.Close()
		return nil, nil, err
	}
	return service, store, nil
}

func writeAPIKeyError(stderr io.Writer, err error, jsonMode bool) int {
	switch {
	case errors.Is(err, apikey.ErrNotFound), errors.Is(err, apikey.ErrConflict), errors.Is(err, storesqlite.ErrConstraint):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitData, "api_key_request", err.Error(), err), jsonMode)
	case errors.Is(err, storesqlite.ErrBusy):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitTemporary, "api_key_busy", "API key state is busy", err), jsonMode)
	case errors.Is(err, storesqlite.ErrSchema), errors.Is(err, storesqlite.ErrCorrupt), errors.Is(err, storesqlite.ErrUnsupportedSchema), errors.Is(err, storesqlite.ErrUnsupportedRevision), errors.Is(err, storesqlite.ErrMissing), errors.Is(err, storesqlite.ErrPermission):
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitPolicy, "api_key_state_refused", "API key state is unavailable", err), jsonMode)
	default:
		return writeErrorMode(stderr, apperror.Wrap(apperror.ExitFailure, "api_key_failed", "API key operation failed", err), jsonMode)
	}
}
