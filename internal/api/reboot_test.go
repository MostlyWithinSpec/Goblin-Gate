package api

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStatusReportsRebootRequired(t *testing.T) {
	old := rebootRequiredPath
	rebootRequiredPath = filepath.Join(t.TempDir(), "reboot-required")
	t.Cleanup(func() { rebootRequiredPath = old })
	a := newTestAPI(t)

	if out := a.expect("GET", "/api/v1/system/status", nil, 200); out["reboot_required"] != false || out["reboot_packages"] != nil {
		t.Fatalf("no restart needed: %v %v", out["reboot_required"], out["reboot_packages"])
	}
	os.WriteFile(rebootRequiredPath, []byte("*** System restart required ***\n"), 0o644)
	os.WriteFile(rebootRequiredPath+".pkgs", []byte("linux-image-6.1.0-54-amd64\nlibc6\nlibc6\n"), 0o644)
	out := a.expect("GET", "/api/v1/system/status", nil, 200)
	pkgs, _ := out["reboot_packages"].([]any)
	if out["reboot_required"] != true || len(pkgs) != 2 || pkgs[0] != "linux-image-6.1.0-54-amd64" || pkgs[1] != "libc6" {
		t.Fatalf("restart needed: %v %v", out["reboot_required"], out["reboot_packages"])
	}
}
