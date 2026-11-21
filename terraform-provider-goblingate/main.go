// Command terraform-provider-goblingate is the Terraform / OpenTofu provider
// for Goblin Gate.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/networkgoblinlabs/goblin-gate/terraform-provider-goblingate/internal/provider"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "run with support for debuggers such as delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/networkgoblinlabs/goblingate",
		Debug:   debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
