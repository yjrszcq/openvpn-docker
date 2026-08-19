package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/yjrszcq/openvpn-docker/internal/apikey"
	"github.com/yjrszcq/openvpn-docker/internal/artifact"
	clientservice "github.com/yjrszcq/openvpn-docker/internal/client"
	"github.com/yjrszcq/openvpn-docker/internal/compatibility"
	configservice "github.com/yjrszcq/openvpn-docker/internal/config"
	configurationservice "github.com/yjrszcq/openvpn-docker/internal/configuration"
	"github.com/yjrszcq/openvpn-docker/internal/domain"
	"github.com/yjrszcq/openvpn-docker/internal/httpapi"
	"github.com/yjrszcq/openvpn-docker/internal/initialize"
	"github.com/yjrszcq/openvpn-docker/internal/pki"
	"github.com/yjrszcq/openvpn-docker/internal/render"
	runtimecontrol "github.com/yjrszcq/openvpn-docker/internal/runtime"
	statecontrol "github.com/yjrszcq/openvpn-docker/internal/state"
	storesqlite "github.com/yjrszcq/openvpn-docker/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Printf("ovpn-api: %v", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddress := os.Getenv("OVPN_API_LISTEN")
	if listenAddress == "" {
		return fmt.Errorf("OVPN_API_LISTEN is required")
	}
	dataDir := os.Getenv("OVPN_DATA_DIR")
	if dataDir == "" {
		dataDir = initialize.DefaultDataDir
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	database, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return fmt.Errorf("open authoritative state: %w", err)
	}
	defer database.Close()
	instance, err := database.LoadOnlyInstance(ctx)
	if err != nil {
		return fmt.Errorf("load instance: %w", err)
	}
	authenticator, err := apikey.NewService(database, instance.ID)
	if err != nil {
		return err
	}
	origins, err := parseOrigins(os.Getenv("OVPN_API_CORS_ORIGINS"))
	if err != nil {
		return err
	}
	resources, err := newResources(database, instance.ID, dataDir)
	if err != nil {
		return err
	}
	handler, err := httpapi.NewHandler(authenticator, origins, resources)
	if err != nil {
		return err
	}
	server, err := httpapi.NewServer(listenAddress, handler)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", listenAddress)
	if err != nil {
		return fmt.Errorf("listen on API address: %w", err)
	}
	log.Printf("ovpn-api: listening on %s", listener.Addr())
	return httpapi.Serve(ctx, listener, server)
}

func newResources(database *storesqlite.Store, instanceID, dataDir string) (httpapi.Resources, error) {
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		return httpapi.Resources{}, err
	}
	clients, err := clientservice.NewService(database, local)
	if err != nil {
		return httpapi.Resources{}, err
	}
	contractPath := environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath)
	contract, err := compatibility.Load(contractPath)
	if err != nil {
		return httpapi.Resources{}, fmt.Errorf("load compatibility contract: %w", err)
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return httpapi.Resources{}, err
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	runner, err := pki.NewRunner(pki.Config{
		EasyRSABinary: apiEasyRSABinary(),
		OpenVPNBinary: environmentOr("OVPN_OPENVPN_BIN", "openvpn"),
	}, nil)
	if err != nil {
		return httpapi.Resources{}, err
	}
	manager, err := clientservice.NewManager(database, local, runner, renderer, render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir})
	if err != nil {
		return httpapi.Resources{}, err
	}
	stateOptions := statecontrol.Options{
		DataDir: dataDir, ConfigFile: environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath),
		ServerName: initialize.DefaultServerName, Renderer: renderer,
		Paths: render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir},
	}
	configurationQueries, err := configurationservice.NewService(database)
	if err != nil {
		return httpapi.Resources{}, err
	}
	configPath := environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath)
	loadConfigState := func(ctx context.Context, expectedDigest string, expectedRevision uint64) (domain.Config, error) {
		desired, err := configservice.LoadFile(configPath)
		if err != nil {
			return domain.Config{}, err
		}
		digest, err := configservice.Digest(desired)
		if err != nil || digest != expectedDigest {
			return domain.Config{}, configservice.ErrDesiredConflict
		}
		instance, err := database.LoadOnlyInstance(ctx)
		if err != nil {
			return domain.Config{}, err
		}
		if uint64(instance.Applied.Revision) != expectedRevision {
			return domain.Config{}, configurationservice.ErrPlanConflict
		}
		return desired, nil
	}
	return httpapi.Resources{
		Version: func() httpapi.VersionResponse { return httpapi.NewVersionResponse(contract) },
		State: func(ctx context.Context) (statecontrol.Report, error) {
			return statecontrol.Scan(ctx, stateOptions), nil
		},
		Clients:   clients,
		Mutations: manager,
		Runtime: func(ctx context.Context) (runtimecontrol.Status, error) {
			identities, err := database.ClientIdentities(ctx, instanceID)
			if err != nil {
				return runtimecontrol.Status{}, err
			}
			return runtimecontrol.QueryStatus(ctx, runtimecontrol.SocketPath(runtimeDir), identities)
		},
		Events: func(ctx context.Context, lines int) ([]runtimecontrol.Event, error) {
			values := make([]runtimecontrol.Event, 0)
			err := runtimecontrol.StreamLines(ctx, runtimecontrol.StreamOptions{Path: filepath.Join(dataDir, "logs", "events.jsonl"), Lines: lines}, func(line string) error {
				event, err := runtimecontrol.ParseEvent(line)
				if err == nil {
					values = append(values, event)
				}
				return err
			})
			return values, err
		},
		Disconnect: func(ctx context.Context, id, name string) (runtimecontrol.DisconnectResult, error) {
			return runtimecontrol.Disconnect(ctx, runtimecontrol.SocketPath(runtimeDir), id, name)
		},
		Applied: func(ctx context.Context) (configservice.AppliedView, error) {
			instance, err := database.LoadOnlyInstance(ctx)
			if err != nil {
				return configservice.AppliedView{}, err
			}
			return configservice.Show(instance.Applied)
		},
		Desired: func(context.Context) (configservice.DesiredView, error) { return configservice.LoadDesired(configPath) },
		PutDesired: func(_ context.Context, expected string, view configservice.View) (configservice.DesiredView, error) {
			return configservice.UpdateDesiredFile(configPath, expected, view)
		},
		ConfigPlan: func(ctx context.Context) (configurationservice.Plan, error) {
			desired, err := configservice.LoadFile(configPath)
			if err != nil {
				return configurationservice.Plan{}, err
			}
			return configurationQueries.Plan(ctx, desired)
		},
		ConfigApply: func(ctx context.Context, input httpapi.ConfigApplyRequest) (configurationservice.ApplyResult, error) {
			desired, err := loadConfigState(ctx, input.DesiredDigest, input.CurrentRevision)
			if err != nil {
				return configurationservice.ApplyResult{}, err
			}
			plan, err := configurationQueries.Plan(ctx, desired)
			if err != nil {
				return configurationservice.ApplyResult{}, err
			}
			if !input.Force {
				options := stateOptions
				options.ConfigFile = ""
				report := statecontrol.Scan(ctx, options)
				if report.State != statecontrol.Healthy {
					return configurationservice.ApplyResult{}, configurationservice.ErrPlanConflict
				}
			}
			if plan.Configuration.InSync {
				return configurationservice.ApplyResult{
					Version: 1,
					Activation: configurationservice.ActivationReport{
						ProfileRedistribution: append(make([]configurationservice.ClientRef, 0, len(plan.ProfileRedistribution)), plan.ProfileRedistribution...),
					},
					Plan: plan,
				}, nil
			}
			session, err := runtimecontrol.BeginApply(ctx, runtimeDir)
			if err != nil {
				return configurationservice.ApplyResult{}, err
			}
			desired, err = loadConfigState(ctx, input.DesiredDigest, input.CurrentRevision)
			var result configurationservice.ApplyResult
			if err == nil {
				result, err = configurationservice.ApplyPersistent(ctx, desired, renderer, render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir})
			}
			resumeCtx, cancelResume := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancelResume()
			if resumeErr := session.Resume(resumeCtx); resumeErr != nil {
				return configurationservice.ApplyResult{}, resumeErr
			}
			if err == nil {
				result.Activation.RuntimeRestarted = true
				result.Activation.RestartRequired = false
			}
			return result, err
		},
	}, nil
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func apiEasyRSABinary() string {
	if value := os.Getenv("OVPN_EASYRSA_BIN"); value != "" {
		return value
	}
	if info, err := os.Stat("/usr/share/easy-rsa/easyrsa"); err == nil && info.Mode().IsRegular() {
		return "/usr/share/easy-rsa/easyrsa"
	}
	return "easyrsa"
}

func parseOrigins(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	parts := strings.Split(value, ",")
	for index := range parts {
		parts[index] = strings.TrimSpace(parts[index])
		if parts[index] == "" {
			return nil, fmt.Errorf("OVPN_API_CORS_ORIGINS contains an empty origin")
		}
	}
	return parts, nil
}
