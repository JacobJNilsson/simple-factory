// Command sfy runs coding agents as interactive sessions in terminal panes.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/JacobJNilsson/simple-factory/internal/cli"
	"github.com/JacobJNilsson/simple-factory/internal/envrun"
	"github.com/JacobJNilsson/simple-factory/internal/up"
)

// version is the release version. The release build sets it with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	// The exit code is the process result, so main exits with it after run returns.
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println(version)
		return 0
	}
	if len(args) > 0 && args[0] == "serve" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
		// After the first signal the default action returns, so a second signal ends the process.
		go func() {
			<-ctx.Done()
			stop()
		}()
		return report(up.Serve(ctx, os.Getenv, os.Stderr))
	}
	if len(args) > 0 && args[0] == "run" {
		// On success the process becomes the command, so run returns only on an error.
		return report(envrun.Run(args[1:]))
	}
	if len(args) > 0 && args[0] == "up" {
		return report(up.Command(os.Environ(), os.Getenv, os.Stdout, os.Stderr))
	}
	if len(args) > 0 && args[0] == "restart" {
		update := len(args) == 2 && args[1] == "--update"
		if len(args) > 1 && !update {
			return report(fmt.Errorf("usage: sfy restart [--update]"))
		}
		return report(up.Restart(os.Environ(), os.Getenv, os.Stdout, os.Stderr, update))
	}
	return cli.Run(args, os.Getenv, os.Stdin, os.Stdout, os.Stderr)
}

func report(err error) int {
	if err != nil {
		fmt.Fprintf(os.Stderr, "sfy: %v\n", err)
		return 1
	}
	return 0
}
