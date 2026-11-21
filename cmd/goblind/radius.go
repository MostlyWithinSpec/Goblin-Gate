package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/api"
	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/devices"
	"github.com/networkgoblinlabs/goblin-gate/internal/radius"
	caddyrender "github.com/networkgoblinlabs/goblin-gate/internal/render/caddy"
	radiusrender "github.com/networkgoblinlabs/goblin-gate/internal/render/radius"
)

// newRadiusState keeps PPSKs and certificates in data_dir/radius.
func newRadiusState(dataDir string) *radius.State {
	ca := caddyrender.StorageDir + "/pki/authorities/" + caddyrender.CAID + "/"
	return &radius.State{
		Path:     filepath.Join(dataDir, "radius", "state.json"),
		RootCert: ca + "root.crt",
		RootKey:  ca + "root.key",
	}
}

// radiusRenderer issues the 802.1X server certificate for the box's name.
func radiusRenderer(dataDir string, st *radius.State) (radiusrender.Renderer, func() (string, error)) {
	token := boxSecret(filepath.Join(dataDir, "radius", "hook.secret"))
	return radiusrender.Renderer{
		Token: token,
		Certs: func(cfg *config.Config) (radiusrender.Certs, error) {
			name := cfg.System.Hostname
			if name == "" {
				name = "goblin-gate"
			}
			c, k, ca, err := st.ServerCert(name)
			return radiusrender.Certs{Cert: c, Key: k, CA: ca}, err
		},
	}, token
}

// startRadius serves FreeRADIUS's hook on loopback.
func startRadius(ctx context.Context, m *radius.Manager, token func() (string, error), logger *slog.Logger) {
	secret, err := token()
	if err != nil {
		logger.Warn("radius: no hook password; RADIUS answers nothing", "error", err)
		return
	}
	srv := &http.Server{
		Handler:           m.Hook(radiusrender.HookUser, secret),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	go func() {
		var ln net.Listener
		for {
			ln, err = net.Listen("tcp", radiusrender.HookAddr)
			if err == nil {
				break
			}
			logger.Warn("radius: hook listen", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Second):
			}
		}
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("radius: hook stopped", "error", err)
		}
	}()
	go func() { <-ctx.Done(); srv.Close() }()
}

// radiusAnnotate hands RADIUS's view of where devices are to the inventory.
func radiusAnnotate(store *devices.AnnotationStore, logger *slog.Logger) func(map[string]map[string]string) {
	return func(in map[string]map[string]string) {
		set := make(map[string]devices.Fields, len(in))
		for mac, f := range in {
			set[radius.ColonMAC(mac)] = devices.Fields(f)
		}
		checked, err := devices.CheckAnnotations(set)
		if err != nil {
			logger.Warn("radius: device annotations", "error", err)
			return
		}
		if _, err := store.Set(api.RadiusAnnotations, checked); err != nil {
			logger.Warn("radius: device annotations", "error", err)
		}
	}
}
