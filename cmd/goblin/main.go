// Command goblin is the FortiOS-style CLI client for Goblin Gate.
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
	"github.com/networkgoblinlabs/goblin-gate/internal/version"
	"golang.org/x/term"
)

// localSocket is goblind's root-only API socket on the box itself.
const localSocket = "/run/goblind/api.sock"

var (
	defaultToken = os.Getenv("GOBLIN_TOKEN")
	// insecureTLS skips certificate checks (goblind's self-signed cert).
	insecureTLS = false
)

// defaultServer is the local socket when it exists (running on the box as
// root), otherwise a dev goblind on localhost.
func defaultServer() string {
	if _, err := os.Stat(localSocket); err == nil {
		return "unix://" + localSocket
	}
	return "http://127.0.0.1:8443"
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	// Parse command-line flags
	var (
		server      = defaultServer()
		token       = defaultToken
		help        = false
		versionFlag = false
		command     = ""
		scriptFile  = ""
		setPassFor  = ""
	)

	// Parse flags
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goblin: missing argument for --server\n")
				return 2
			}
			server = args[i+1]
			i += 2
		case "--token":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goblin: missing argument for --token\n")
				return 2
			}
			token = args[i+1]
			i += 2
		case "--set-password":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goblin: missing argument for --set-password\n")
				return 2
			}
			setPassFor = args[i+1]
			i += 2
		case "--insecure":
			insecureTLS = true
			i++
		case "-h", "--help":
			help = true
			i++
		case "--version":
			versionFlag = true
			i++
		case "-c":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goblin: missing argument for -c\n")
				return 2
			}
			command = args[i+1]
			i += 2
		case "import-pfsense":
			return runImportPfSense(server, token, args[i+1:], os.Stdout)
		case "-f":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "goblin: missing argument for -f\n")
				return 2
			}
			scriptFile = args[i+1]
			i += 2
		default:
			fmt.Fprintf(os.Stderr, "goblin: unknown flag %q\n", args[i])
			return 2
		}
	}

	// Handle version flag
	if versionFlag {
		fmt.Printf("goblin %s\n", version.Version)
		return 0
	}

	// Handle help flag
	if help {
		usage()
		return 0
	}

	if setPassFor != "" {
		return runSetPassword(server, token, setPassFor, os.Stdin, os.Stderr)
	}

	// Handle non-interactive command
	if command != "" {
		return runCommand(server, token, command)
	}

	// Handle script file
	if scriptFile != "" {
		return runScript(server, token, scriptFile)
	}

	// Interactive mode
	return runInteractive(server, token)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: goblin [flags]")
	fmt.Fprintln(os.Stderr, "  --server SERVER  goblind address: https://HOST, http://HOST:PORT or unix:///PATH")
	fmt.Fprintln(os.Stderr, "                   (default: unix://"+localSocket+" if present, else http://127.0.0.1:8443)")
	fmt.Fprintln(os.Stderr, "  --insecure       accept goblind's self-signed TLS certificate")
	fmt.Fprintln(os.Stderr, "  --set-password USER  set a user's password (as root on the box: no old password needed)")
	fmt.Fprintln(os.Stderr, "  --token TOKEN      authentication token (default: GOBLIN_TOKEN env)")
	fmt.Fprintln(os.Stderr, "  -c COMMAND         run single command and exit")
	fmt.Fprintln(os.Stderr, "  -f FILE      run commands from file")
	fmt.Fprintln(os.Stderr, "  --version       print version and exit")
	fmt.Fprintln(os.Stderr, "  -h, --help      show this help")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "examples:")
	fmt.Fprintln(os.Stderr, `  goblin -c "config interfaces edit lan set address 192.168.1.1/24 next end"`)
	fmt.Fprintln(os.Stderr, `  goblin -c "commit"`)
	fmt.Fprintln(os.Stderr, `  goblin -f changes.gob   # run a script of commands`)
	fmt.Fprintln(os.Stderr, `  goblin import-pfsense config.xml --devices wan=eth0,lan=eth1   # pfSense backup -> candidate`)
}

func runCommand(server, token, command string) int {
	client := newGoblinClient(server, token)
	shell := &interactiveShell{client: client}
	if err := shell.executeLine(command); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func runScript(server, token, scriptFile string) int {
	file, err := os.Open(scriptFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "goblin: failed to open script file %s: %v\n", scriptFile, err)
		return 1
	}
	defer file.Close()

	client := newGoblinClient(server, token)
	shell := &interactiveShell{client: client}
	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := shell.executeLine(line); err != nil {
			fmt.Fprintf(os.Stderr, "goblin: error at line %d in %s: %v\n", lineNum, scriptFile, err)
			return 1
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "goblin: error reading script file %s: %v\n", scriptFile, err)
		return 1
	}
	return 0
}

func runBackup(server, token, command string) int {
	fmt.Println("Backup functionality not implemented in CLI yet")
	return 0
}

func runRestore(server, token, command string) int {
	fmt.Println("Restore functionality not implemented in CLI yet")
	return 0
}

func runInteractive(server, token string) int {
	// Initialize the client
	client := &goblinClient{
		server: server,
		token:  token,
	}

	// Check if we're in a terminal
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintf(os.Stderr, "goblin: not running in a terminal\n")
		return 1
	}

	// Get initial context
	ctx, err := client.getContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "goblin: failed to get initial context: %v\n", err)
		return 1
	}

	// Create interactive shell
	shell := &interactiveShell{
		client: client,
		ctx:    ctx,
	}

	// Start the shell
	return shell.run()
}

type goblinClient struct {
	server string
	token  string
	client *http.Client
}

func newGoblinClient(server, token string) *goblinClient {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if path, ok := strings.CutPrefix(server, "unix://"); ok {
		tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		}
		server = "http://goblind" // the host part is not used
	}
	if insecureTLS {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &goblinClient{
		server: strings.TrimRight(server, "/"),
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second, Transport: tr},
	}
}

func (c *goblinClient) doRequest(method, path string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = strings.NewReader(string(jsonBody))
	}

	req, err := http.NewRequest(method, c.server+path, reqBody)
	if err != nil {
		return nil, err
	}

	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")

	return c.client.Do(req)
}

func (c *goblinClient) getContext() (*contextState, error) {
	// Get the candidate config to understand current context
	resp, err := c.doRequest("GET", "/api/v1/config/candidate", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get candidate config: %d", resp.StatusCode)
	}

	var candidate *config.Config
	if err := json.NewDecoder(resp.Body).Decode(&candidate); err != nil {
		return nil, err
	}

	// For now, just return a basic context state
	return &contextState{
		policy: 10, // Default policy number
	}, nil
}

type contextState struct {
	policy int
}

type interactiveShell struct {
	client *goblinClient
	ctx    *contextState
}

func (s *interactiveShell) run() int {
	// Initialize readline
	stdin := os.Stdin

	// Initialize readline
	termState, err := term.MakeRaw(int(stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "goblin: failed to initialize terminal: %v\n", err)
		return 1
	}
	defer term.Restore(int(stdin.Fd()), termState)

	// Create a scanner for reading input
	scanner := bufio.NewScanner(stdin)

	// Main loop
	for {
		// Print prompt
		fmt.Printf("goblin-gate (policy %d) # ", s.ctx.policy)

		// Read line
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fmt.Fprintf(os.Stderr, "goblin: error reading input: %v\n", err)
			}
			break
		}

		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines
		if line == "" {
			continue
		}

		// Handle exit command
		if strings.ToLower(line) == "exit" || strings.ToLower(line) == "quit" {
			break
		}

		// Handle help command
		if strings.ToLower(line) == "help" {
			s.printHelp()
			continue
		}

		// Parse and handle command
		if err := s.handleCommand(line); err != nil {
			fmt.Fprintf(os.Stderr, "goblin: error: %v\n", err)
		}
	}

	return 0
}

func (s *interactiveShell) handleCommand(line string) error {
	return s.executeLine(line)
}

func (s *interactiveShell) handleDiff() error {
	resp, err := s.client.doRequest("GET", "/api/v1/config/diff", nil)
	if err != nil {
		return fmt.Errorf("failed to get diff: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("diff API returned status %d", resp.StatusCode)
	}

	var diffResp struct {
		Count   int             `json:"count"`
		Changes []config.Change `json:"changes"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&diffResp); err != nil {
		return fmt.Errorf("failed to decode diff response: %v", err)
	}

	fmt.Printf("Changes: %d\n", diffResp.Count)
	for _, change := range diffResp.Changes {
		fmt.Printf("  %s: %s (op=%s)\n", change.Path, s.formatChangeValue(change.Old), change.Op)
	}

	return nil
}

func (s *interactiveShell) formatChangeValue(v any) string {
	if v == nil {
		return "<nil>"
	}

	// Simple formatting for display
	switch val := v.(type) {
	case string:
		return fmt.Sprintf("%q", val)
	case float64:
		return fmt.Sprintf("%v", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func (s *interactiveShell) handleCommit() error {
	// Simple commit without confirmation
	resp, err := s.client.doRequest("POST", "/api/v1/commit", nil)
	if err != nil {
		return fmt.Errorf("failed to commit: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("commit API returned status %d", resp.StatusCode)
	}

	var commitResp struct {
		Revision int `json:"revision"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&commitResp); err != nil {
		return fmt.Errorf("failed to decode commit response: %v", err)
	}

	fmt.Printf("Committed revision %d\n", commitResp.Revision)
	return nil
}

func (s *interactiveShell) handleConfirm() error {
	resp, err := s.client.doRequest("POST", "/api/v1/commit/confirm", nil)
	if err != nil {
		return fmt.Errorf("failed to confirm: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("confirm API returned status %d", resp.StatusCode)
	}

	fmt.Println("Confirmed commit")
	return nil
}

func (s *interactiveShell) handleDiscard() error {
	resp, err := s.client.doRequest("POST", "/api/v1/commit/discard", nil)
	if err != nil {
		return fmt.Errorf("failed to discard: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discard API returned status %d", resp.StatusCode)
	}

	fmt.Println("Discarded candidate config")
	return nil
}

func (s *interactiveShell) handleHistory() error {
	resp, err := s.client.doRequest("GET", "/api/v1/revisions", nil)
	if err != nil {
		return fmt.Errorf("failed to get history: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("history API returned status %d", resp.StatusCode)
	}

	var historyResp struct {
		Revisions []struct {
			Revision int    `json:"revision"`
			User     string `json:"user"`
			Comment  string `json:"comment"`
			Date     string `json:"date"`
		} `json:"revisions"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&historyResp); err != nil {
		return fmt.Errorf("failed to decode history response: %v", err)
	}

	fmt.Printf("History (%d revisions):\n", len(historyResp.Revisions))
	for _, rev := range historyResp.Revisions {
		fmt.Printf("  %d: %s by %s (%s)\n", rev.Revision, rev.Comment, rev.User, rev.Date)
	}

	return nil
}

func (s *interactiveShell) handleRollback(line string) error {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return fmt.Errorf("usage: rollback <revision>")
	}
	rev := fields[1]
	resp, err := s.client.doRequest(http.MethodPost, "/api/v1/revisions/"+rev+"/rollback", nil)
	if err != nil {
		return fmt.Errorf("failed to rollback: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return apiError("rollback "+rev, resp, body)
	}
	fmt.Printf("Rolled back to revision %s\n", rev)
	return nil
}

func (s *interactiveShell) handleSystemStatus() error {
	resp, err := s.client.doRequest("GET", "/api/v1/system/status", nil)
	if err != nil {
		return fmt.Errorf("failed to get system status: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("system status API returned status %d", resp.StatusCode)
	}

	var statusResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&statusResp); err != nil {
		return fmt.Errorf("failed to decode system status response: %v", err)
	}

	fmt.Println("System Status:")
	for k, v := range statusResp {
		fmt.Printf("  %s: %v\n", k, v)
	}

	return nil
}

func (s *interactiveShell) handleBackup() error {
	fmt.Println("Backup functionality not implemented in CLI yet")
	return nil
}

func (s *interactiveShell) handleRestore() error {
	fmt.Println("Restore functionality not implemented in CLI yet")
	return nil
}

func (s *interactiveShell) printHelp() {
	fmt.Println("Available commands:")
	fmt.Println("  diff                 Show changes between running and candidate config")
	fmt.Println("  commit              Commit candidate config")
	fmt.Println("  commit confirm N  Commit with confirmation timeout (N minutes)")
	fmt.Println("  confirm             Confirm pending commit")
	fmt.Println("  discard             Discard candidate config")
	fmt.Println("  history           Show commit history")
	fmt.Println("  rollback N          Rollback to revision N")
	fmt.Println("  get system status     Show system status")
	fmt.Println("  execute backup        Backup configuration")
	fmt.Println("  execute restore     Restore configuration")
	fmt.Println("  exit/quit          Exit the shell")
	fmt.Println("  help                Show this help")
}

// runSetPassword asks for a new password twice (or reads one line when
// stdin is not a terminal) and sets it. Over the local socket (root on the
// box) no current password is needed; elsewhere it is asked for too.
func runSetPassword(server, token, user string, in *os.File, out io.Writer) int {
	interactive := term.IsTerminal(int(in.Fd()))
	reader := bufio.NewReader(in)
	ask := func(prompt string) (string, error) {
		if !interactive {
			line, err := reader.ReadString('\n')
			if err != nil && line == "" {
				return "", err
			}
			return strings.TrimRight(line, "\r\n"), nil
		}
		fmt.Fprint(out, prompt)
		b, err := term.ReadPassword(int(in.Fd()))
		fmt.Fprintln(out)
		return string(b), err
	}
	req := map[string]string{"user": user}
	if !strings.HasPrefix(server, "unix://") {
		cur, err := ask("Current password: ")
		if err != nil {
			fmt.Fprintf(out, "goblin: %v\n", err)
			return 1
		}
		req["current_password"] = cur
	}
	pw, err := ask("New password for " + user + ": ")
	if err != nil {
		fmt.Fprintf(out, "goblin: %v\n", err)
		return 1
	}
	if interactive {
		again, err := ask("Repeat: ")
		if err != nil {
			fmt.Fprintf(out, "goblin: %v\n", err)
			return 1
		}
		if again != pw {
			fmt.Fprintln(out, "goblin: passwords do not match")
			return 1
		}
	}
	req["new_password"] = pw
	resp, err := newGoblinClient(server, token).doRequest("POST", "/api/v1/auth/password", req)
	if err != nil {
		fmt.Fprintf(out, "goblin: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		fmt.Fprintf(out, "goblin: %s (HTTP %d)\n", body.Error.Message, resp.StatusCode)
		return 1
	}
	fmt.Fprintf(out, "Password for %s changed; their sessions were ended.\n", user)
	return 0
}
