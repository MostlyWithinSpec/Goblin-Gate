package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/updates"
)

// updateManager is the box's update manager (task 051).
func updateManager(logger *slog.Logger) *updates.Manager {
	return &updates.Manager{Logger: logger, Health: socketHealth}
}

// socketHealth asks the running goblind for its version over the local API
// socket: the updater's health check after installing a package.
func socketHealth(ctx context.Context) (string, error) {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", SocketPath)
		},
	}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://goblind/api/v1/system/status", nil)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	var st struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return "", err
	}
	return st.Version, nil
}

// runUpdate is `goblind update-run --to <version> [--os]`, started by the
// update manager in a transient systemd unit so it outlives goblind's restart.
func runUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("update-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	to := fs.String("to", "", "goblin-gate version to install (empty with --os: only Debian updates)")
	includeOS := fs.Bool("os", false, "also install pending Debian updates")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	st := updateManager(logger).Run(ctx, *to, *includeOS)
	if st.State != "done" {
		fmt.Fprintf(stderr, "goblind: update %s: %s\n", st.State, st.Message)
		return 1
	}
	return 0
}
