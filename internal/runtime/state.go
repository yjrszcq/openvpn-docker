package runtime

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

var ErrRuntimeState = errors.New("runtime state is invalid")

// LoadInstance opens the authoritative database and verifies the server
// configuration artifact before any runtime process is started.
func LoadInstance(ctx context.Context, dataDir string) (storesqlite.InstanceState, error) {
	database, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return storesqlite.InstanceState{}, err
	}
	defer database.Close()
	instance, err := database.LoadOnlyInstance(ctx)
	if err != nil {
		return storesqlite.InstanceState{}, err
	}
	metadata, err := database.LoadInstanceArtifacts(ctx, instance.ID)
	if err != nil {
		return storesqlite.InstanceState{}, err
	}
	var serverConfig *storesqlite.ArtifactMetadata
	for index := range metadata {
		candidate := &metadata[index]
		if candidate.Kind == "server-config" && candidate.Status == storesqlite.ArtifactActive {
			if serverConfig != nil {
				return storesqlite.InstanceState{}, fmt.Errorf("%w: multiple active server configurations", ErrRuntimeState)
			}
			serverConfig = candidate
		}
	}
	if serverConfig == nil || serverConfig.Key != "server/server.conf" {
		return storesqlite.InstanceState{}, fmt.Errorf("%w: active server configuration metadata is missing", ErrRuntimeState)
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		return storesqlite.InstanceState{}, err
	}
	_, reference, err := local.Read(ctx, serverConfig.Key)
	if err != nil || reference.Digest != serverConfig.Digest || reference.Mode != 0o600 {
		return storesqlite.InstanceState{}, fmt.Errorf("%w: server configuration does not match SQLite metadata", ErrRuntimeState)
	}
	return instance, nil
}
