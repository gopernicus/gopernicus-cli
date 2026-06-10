package main

import (
	"context"
	"fmt"
	"os"

	"github.com/gopernicus/gopernicus-cli/cmd"
	"github.com/gopernicus/gopernicus/workshop/codegen/goversion"
)

func main() {
	if err := goversion.Check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := cmd.Execute(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
