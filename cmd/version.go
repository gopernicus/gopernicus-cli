package cmd

import (
	"context"
	"fmt"
	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
)

func init() {
	cli.RegisterCommand(&cli.Command{
		Name:  "version",
		Short: "Show gopernicus CLI version",
		Long:  "Show the gopernicus CLI version and build information.",
		Usage: "gopernicus version",
		Run:   runVersion,
	})
}

func runVersion(_ context.Context, _ []string) error {
	fmt.Printf("gopernicus %s\n", cli.Version)
	return nil
}
