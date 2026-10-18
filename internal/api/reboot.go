package api

import (
	"os"
	"slices"
	"strings"
)

// rebootRequiredPath is where Debian's packages (the kernel, libc, systemd)
// note that a restart is needed to use their update; tests replace it.
var rebootRequiredPath = "/run/reboot-required"

// rebootRequired reports whether a restart is needed and which packages
// asked for it (the list can be empty when Debian did not record them).
func rebootRequired() (bool, []string) {
	if _, err := os.Stat(rebootRequiredPath); err != nil {
		return false, nil
	}
	pkgs := []string{}
	b, _ := os.ReadFile(rebootRequiredPath + ".pkgs")
	for _, p := range strings.Fields(string(b)) {
		if len(pkgs) < 20 && !slices.Contains(pkgs, p) {
			pkgs = append(pkgs, p)
		}
	}
	return true, pkgs
}
