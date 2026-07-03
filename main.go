// Package main defines the Jenkins Terraform Provider entrypoint.
//
// See https://registry.terraform.io/providers/namecheap/jenkins for usage documentation.
package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/namecheap/terraform-provider-jenkins/jenkins"
)

const providerAddress = "registry.terraform.io/namecheap/jenkins"

func main() {
	var debug bool

	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: providerAddress,
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), jenkins.New, opts); err != nil {
		log.Fatal(err.Error())
	}
}
