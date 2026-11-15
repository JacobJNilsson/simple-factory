// Command factory runs coding agents as interactive sessions in terminal panes.
package main

import (
	"fmt"
	"os"

	"github.com/JacobJNilsson/factory/internal/cli"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "serve" || os.Args[1] == "up") {
		fmt.Fprintln(os.Stderr, "factory: not implemented yet")
		os.Exit(1)
	}
	os.Exit(cli.Run(os.Args[1:], os.Getenv, os.Stdin, os.Stdout, os.Stderr))
}
