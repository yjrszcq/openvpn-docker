package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/yjrszcq/openvpn-docker/internal/apperror"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
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

func usageError(message string) error {
	return apperror.New(apperror.ExitUsage, "usage", message)
}
