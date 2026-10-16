package rendertest

import (
	"testing"

	"github.com/networkgoblinlabs/goblin-gate/internal/config"
)

func TestMultiWANIsValid(t *testing.T) {
	if errs := config.Validate(MultiWAN()); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
}
