//go:build linux

package nft

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestNftSyntax runs `nft -c -f` (check only, nothing is applied) on every
// golden file. It needs root and nftables, so it is opt-in:
//
//	sudo GOBLIN_NFT_CHECK=1 go test ./internal/render/nft -run TestNftSyntax -v
func TestNftSyntax(t *testing.T) {
	if os.Getenv("GOBLIN_NFT_CHECK") != "1" {
		t.Skip("set GOBLIN_NFT_CHECK=1 (as root, with nft installed) to syntax-check golden files")
	}
	nft, err := exec.LookPath("nft")
	if err != nil {
		t.Fatalf("nft not found: %v", err)
	}
	files, err := filepath.Glob(filepath.Join("testdata", "*.nft"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no golden files: %v", err)
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			out, err := exec.Command(nft, "-c", "-f", f).CombinedOutput()
			if err != nil {
				t.Fatalf("nft -c -f %s: %v\n%s", f, err, out)
			}
		})
	}
}
