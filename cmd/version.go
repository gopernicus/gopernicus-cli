package cmd

import (
	"context"
	"fmt"
)

func init() {
	RegisterCommand(&Command{
		Name:  "version",
		Short: "Show gopernicus CLI version",
		Long:  "Show the gopernicus CLI version and build information.",
		Usage: "gopernicus version",
		Run:   runVersion,
	})
}

func runVersion(_ context.Context, _ []string) error {
	fmt.Printf("gopernicus %s\n", Version)
	return nil
}
