package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/opper-ai/terraform-provider-opper/internal/provider"
)

// Set by release builds with -ldflags.
var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "Run with debugger support")
	flag.Parse()
	if err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/opper-ai/opper",
		Debug:   debug,
	}); err != nil {
		log.Fatal(err)
	}
}
