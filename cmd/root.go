package cmd

import (
	"context"

	"github.com/gopernicus/gopernicus/workshop/codegen/cli"
	"github.com/gopernicus/gopernicus/workshop/codegen/manifest"
	"github.com/gopernicus/gopernicus/workshop/codegen/project"
)

// Execute runs the registered commands against os.Args.
func Execute(ctx context.Context) error {
	return cli.Execute(ctx)
}

// loadProject locates the project root and loads its manifest.
func loadProject() (string, *manifest.Manifest, error) {
	root, err := project.MustFindRoot()
	if err != nil {
		return "", nil, err
	}
	m, err := manifest.Load(root)
	if err != nil {
		return "", nil, err
	}
	return root, m, nil
}
