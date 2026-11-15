// Command sfy runs coding agents as interactive sessions in terminal panes.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/JacobJNilsson/simple-factory/internal/cli"
	"github.com/JacobJNilsson/simple-factory/internal/up"
)

func main() {
	// The exit code is the process result, so main exits with it after run returns.
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
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
	if len(args) > 0 && args[0] == "up" {
		return report(up.Command(os.Environ(), os.Getenv, os.Stdout))
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
