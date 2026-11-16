package pluginsdk

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// Env is what goblind tells a plugin through its unit's environment.
type Env struct {
	Name      string // GOBLIN_PLUGIN_NAME
	Socket    string // GOBLIN_PLUGIN_SOCKET: goblind's plugin API
	APISocket string // GOBLIN_PLUGIN_API_SOCKET: where the plugin may serve its own API for the admin UI
	StateDir  string // STATE_DIRECTORY: the plugin's own persistent directory
}

// FromEnv reads Env; outside goblind (a developer's shell) the socket
// defaults to DefaultSocket and the state directory to ".".
func FromEnv() Env {
	e := Env{
		Name:      os.Getenv("GOBLIN_PLUGIN_NAME"),
		Socket:    os.Getenv("GOBLIN_PLUGIN_SOCKET"),
		APISocket: os.Getenv("GOBLIN_PLUGIN_API_SOCKET"),
		StateDir:  os.Getenv("STATE_DIRECTORY"),
	}
	if e.Socket == "" {
		e.Socket = DefaultSocket
	}
	if e.StateDir == "" {
		e.StateDir = "."
	}
	return e
}

// ManifestDir is where installed manifests live.
const ManifestDir = "/usr/share/goblin-gate/plugins"

// InstalledVersion returns the version from the installed manifest of the
// plugin name, "" when it cannot be read.
func InstalledVersion(name string) string {
	data, err := os.ReadFile(filepath.Join(ManifestDir, name, "plugin.json"))
	if err != nil {
		return ""
	}
	var m struct {
		Version string `json:"version"`
	}
	_ = json.Unmarshal(data, &m)
	return m.Version
}

// Plugin is a running plugin, handed to the function Run calls.
type Plugin struct {
	Env    Env
	Client *Client
	Self   Self // approved capabilities and settings, read once at start
	Logger *slog.Logger
}

// WaitSelf reads Self, retrying while goblind is not answering yet (it may
// still be starting when the plugin is). It gives up after tries attempts
// delay apart, or when ctx ends. An answer that is an API error (goblind is up
// but refuses) is returned at once.
func (c *Client) WaitSelf(ctx context.Context, tries int, delay time.Duration) (Self, error) {
	for i := 1; ; i++ {
		s, err := c.Self(ctx)
		if err == nil || StatusOf(err) != 0 || i >= tries {
			return s, err
		}
		select {
		case <-ctx.Done():
			return s, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// Run runs a plugin: it sets up logging to stderr (the journal), a context
// that SIGTERM and SIGINT cancel, the client from the environment and Self,
// then calls fn. When fn returns an error, Run logs it and exits with status
// 1, so systemd restarts the plugin (Restart=on-failure); a nil return exits 0.
func Run(fn func(ctx context.Context, p *Plugin) error) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := run(context.Background(), FromEnv(), logger, fn); err != nil {
		logger.Error("plugin stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, env Env, logger *slog.Logger, fn func(context.Context, *Plugin) error) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	client := NewClient(env.Socket)
	self, err := client.WaitSelf(ctx, 30, 2*time.Second)
	if err != nil {
		if ctx.Err() != nil {
			return nil // stopped while waiting
		}
		return err
	}
	if env.Name == "" {
		env.Name = self.Name
	}
	return fn(ctx, &Plugin{Env: env, Client: client, Self: self, Logger: logger.With("plugin", env.Name)})
}

// ServeAPI serves h for the admin UI on the plugin's API socket until ctx
// ends: goblind forwards /api/v1/plugins/<name>/api/<path> to it as /<path>,
// with the admin's user name and role in X-Goblin-User and X-Goblin-Role.
// Answers must be JSON. It returns at once, without error, when goblind gave
// the plugin no API socket.
func (p *Plugin) ServeAPI(ctx context.Context, h http.Handler) error {
	if p.Env.APISocket == "" {
		return nil
	}
	_ = os.Remove(p.Env.APISocket) // left over from the last run
	ln, err := net.Listen("unix", p.Env.APISocket)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sc)
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
