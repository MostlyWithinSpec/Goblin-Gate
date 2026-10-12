package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/networkgoblinlabs/goblin-gate/internal/cli"
)

// executeLine runs one raw CLI line against the server, printing output and
// returning an error on failure. It is shared by -c, -f and the interactive
// shell so all three behave the same way.
func (s *interactiveShell) executeLine(line string) error {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return nil
	}
	lower := strings.ToLower(trimmed)
	switch {
	case lower == "diff":
		return s.handleDiff()
	case lower == "commit":
		return s.handleCommit()
	case lower == "confirm":
		return s.handleConfirm()
	case lower == "discard":
		return s.handleDiscard()
	case lower == "history":
		return s.handleHistory()
	case strings.HasPrefix(lower, "rollback"):
		return s.handleRollback(trimmed)
	case strings.HasPrefix(lower, "get system status") || lower == "status":
		return s.handleSystemStatus()
	case strings.HasPrefix(lower, "config ") || lower == "config":
		return s.executeConfigBlock(trimmed)
	case strings.HasPrefix(lower, "show "):
		return s.executeShow(trimmed)
	default:
		return fmt.Errorf("unknown command %q (try help)", firstWord(trimmed))
	}
}

// executeConfigBlock parses a `config ...` block and sends one PATCH per
// set/unset/append/delete operation.
func (s *interactiveShell) executeConfigBlock(line string) error {
	tok := cli.NewTokenizer(line)
	parsed, err := cli.NewParser(tok.Tokens()).Parse()
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	for _, op := range parsed {
		if err := s.applyOperation(op); err != nil {
			return err
		}
	}
	return nil
}

// executeShow parses a `show ...` line and prints the matching candidate
// section as JSON.
func (s *interactiveShell) executeShow(line string) error {
	tok := cli.NewTokenizer(line)
	parsed, err := cli.NewParser(tok.Tokens()).Parse()
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}
	if len(parsed) == 0 || parsed[0].Type != cli.OpShow {
		return fmt.Errorf("expected a show command")
	}
	op := parsed[0]
	resp, err := s.client.doRequest(http.MethodGet, "/api/v1/config/candidate", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return apiError("show", resp, body)
	}
	var candidate map[string]json.RawMessage
	if err := json.Unmarshal(body, &candidate); err != nil {
		return fmt.Errorf("decode candidate: %w", err)
	}
	section := showSectionKey(op.Section)
	if section == "" {
		out, _ := json.MarshalIndent(candidate, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	raw, ok := candidate[section]
	if !ok {
		fmt.Printf("%s: (empty)\n", section)
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("decode %s: %w", section, err)
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
	return nil
}

// showSectionKey maps a CLI section name (e.g. "system interface") to the
// top-level candidate JSON key (e.g. "interfaces"). An empty section returns
// an empty string, meaning "show everything".
func showSectionKey(section string) string {
	switch strings.TrimSpace(strings.ToLower(section)) {
	case "":
		return ""
	case "system interface", "interfaces":
		return "interfaces"
	case "firewall policy", "policies":
		return "policies"
	case "firewall address", "addresses":
		return "addresses"
	case "system dhcp server", "dhcp":
		return "dhcp"
	case "firewall service custom", "services":
		return "services"
	case "firewall addrgrp", "address_groups":
		return "address_groups"
	case "firewall vip", "nat.port_forwards":
		return "nat.port_forwards"
	default:
		return section
	}
}

// showSection maps a parser path like "interfaces.lan" or "policies" to the
// top-level candidate key.
func showSection(path string) string {
	if i := strings.IndexByte(path, '.'); i >= 0 {
		return path[:i]
	}
	return path
}

// applyOperation converts one parser operation into a PATCH request.
func (s *interactiveShell) applyOperation(op cli.Operation) error {
	path, err := opToPatchPath(op)
	if err != nil {
		return err
	}
	var reqBody map[string]any
	switch op.Type {
	case cli.OpSet:
		reqBody = map[string]any{"path": path, "value": coerceValue(op.Value)}
	case cli.OpUnset:
		reqBody = map[string]any{"path": path, "delete": true}
	case cli.OpAppend:
		reqBody = map[string]any{"path": path, "append": op.Value}
	default:
		return fmt.Errorf("unsupported operation %v", op.Type)
	}
	resp, err := s.client.doRequest(http.MethodPatch, "/api/v1/config/candidate", reqBody)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode/100 != 2 {
		return apiError("PATCH "+path, resp, body)
	}
	var out struct {
		Status           string `json:"status"`
		ValidationErrors []any  `json:"validation_errors"`
	}
	if err := json.Unmarshal(body, &out); err == nil && len(out.ValidationErrors) > 0 {
		fmt.Printf("warning: candidate has validation errors:\n")
		for _, e := range out.ValidationErrors {
			fmt.Printf("  %v\n", e)
		}
	}
	return nil
}

// opToPatchPath converts a parser path (interfaces.lan.address) to the
// bracket form used by config.ParsePath (interfaces[name=lan].address).
// Policies use [id=N] instead of [name=N].
func opToPatchPath(op cli.Operation) (string, error) {
	parts := strings.Split(op.Path, ".")
	if len(parts) < 3 {
		return "", fmt.Errorf("path %q has no field to set", op.Path)
	}
	section := parts[0]
	name := parts[len(parts)-2]
	field := parts[len(parts)-1]
	key := "name"
	if section == "policies" {
		key = "id"
	}
	return fmt.Sprintf("%s[%s=%s].%s", section, key, name, field), nil
}

// coerceValue turns a CLI string into the JSON value it most likely means:
// true/false, integers and floats when they parse cleanly, else the raw
// string.
func coerceValue(s string) any {
	switch strings.ToLower(s) {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// apiError formats a non-2xx API response using the ErrorBody shape.
func apiError(what string, resp *http.Response, body []byte) error {
	var eb struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Details any    `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &eb); err == nil && eb.Error.Message != "" {
		msg := fmt.Sprintf("%s: %s (%s)", what, eb.Error.Message, resp.Status)
		if eb.Error.Details != nil {
			d, _ := json.Marshal(eb.Error.Details)
			msg += " details: " + string(d)
		}
		return errors.New(msg)
	}
	return fmt.Errorf("%s: %s", what, resp.Status)
}

// firstWord returns the first whitespace-delimited word of s.
func firstWord(s string) string {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s
	}
	return s[:i]
}
