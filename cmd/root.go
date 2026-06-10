package cmd

import (
	"context"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
)

// Execute runs the registered commands against os.Args.
func Execute(ctx context.Context) error {
	return cli.Execute(ctx)
}
