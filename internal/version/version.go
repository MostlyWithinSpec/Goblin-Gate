// Package version holds the build version of the Goblin Gate binaries.
package version

// Version is the version of the running binary. It defaults to a development
// placeholder and is meant to be overridden at build time with, for example:
//
//	go build -ldflags "-X github.com/networkgoblinlabs/goblin-gate/internal/version.Version=1.2.3"
var Version = "0.0.0-dev"
