package logexport

import (
	"fmt"
	"strings"
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestRedactStripsSecrets(t *testing.T) {
	c := config.LogExport{
		Name:     "test",
		Type:     "loki",
		URL:      "http://user:p4ss@127.0.0.1:9/loki/api/v1/push",
		Username: "user",
		Password: "hunter2",
		Token:    "tok-abc",
	}

	errs := []error{
		fmt.Errorf("push failed with password hunter2"),
		fmt.Errorf("auth token tok-abc rejected"),
		fmt.Errorf("bad url http://user:p4ss@127.0.0.1:9/loki/api/v1/push"),
		fmt.Errorf("combined: hunter2 and tok-abc in http://user:p4ss@127.0.0.1:9"),
	}

	for _, err := range errs {
		got := redact(c, err).Error()
		for _, secret := range []string{"hunter2", "tok-abc", "p4ss", "user:p4ss@"} {
			if strings.Contains(got, secret) {
				t.Errorf("redact leaked %q in: %s", secret, got)
			}
		}
	}
}

func TestRedactNilError(t *testing.T) {
	c := config.LogExport{Password: "x", Token: "y"}
	if got := redact(c, nil); got != nil {
		t.Fatalf("redact(nil) = %v, want nil", got)
	}
}

func TestStripUserinfo(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"http://user:pass@host:9030/loki/api/v1/push", "http://host:9030/loki/api/v1/push"},
		{"https://user@host/path", "https://host/path"},
		{"http://host:9030/loki/api/v1/push", "http://host:9030/loki/api/v1/push"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := stripUserinfo(tc.in); got != tc.want {
			t.Errorf("stripUserinfo(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
