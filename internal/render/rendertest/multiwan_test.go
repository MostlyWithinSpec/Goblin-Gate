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

func TestSitesIsValid(t *testing.T) {
	if errs := config.Validate(Sites()); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
}
func TestPortalIsValid(t *testing.T) {
	if errs := config.Validate(Portal()); len(errs) > 0 {
		t.Fatalf("fixture invalid: %v", errs)
	}
}

func TestRadiusIsValid(t *testing.T) {
	if err := config.Validate(Radius()); err != nil {
		t.Fatal(err)
	}
}
