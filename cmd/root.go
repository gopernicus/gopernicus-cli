package cmd

import (
	"context"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"

	// Register the shared gopernicus commands (generate, db, new, boot,
	// doctor, version) provided by the framework.
	_ "github.com/gopernicus/gopernicus/workshop/gopernicus/commands"
)

// Execute runs the registered commands against os.Args.
func Execute(ctx context.Context) error {
	return cli.Execute(ctx)
}
