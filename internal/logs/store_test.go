package logs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	// Create a temporary database file
	tmpDir, err := os.MkdirTemp("", "logstore-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	// Test Insert
	event := &Event{
		Time:      time.Now().Unix(),
		Kind:      "traffic",
		Action:    "allow",
		PolicyID:  "policy1",
		SrcIP:     "192.168.1.100",
		SrcPort:   12345,
		DstIP:     "10.0.0.1",
		DstPort:   80,
		Proto:     "TCP",
		InIntf:    "eth0",
		OutIntf:   "eth1",
		Bytes:     1024,
		App:       "http",
		Category:  "web",
		Signature: "test",
		Severity:  "low",
		Message:   "test message",
	}

	if err := store.Insert(event); err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	// Test Rollup
	if err := store.Rollup(); err != nil {
		t.Fatalf("failed to rollup events: %v", err)
	}

	// Test Retention
	if err := store.Retention(1); err != nil {
		t.Fatalf("failed to run retention: %v", err)
	}
}
