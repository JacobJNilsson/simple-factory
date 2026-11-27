// Command sfy runs coding agents as interactive sessions in terminal panes.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"github.com/JacobJNilsson/simple-factory/internal/cli"
	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/envrun"
	"github.com/JacobJNilsson/simple-factory/internal/up"
)

// version is the release version. The release build sets it with -ldflags "-X main.version=<tag>".
var version = "dev"

func main() {
	// The exit code is the process result, so main exits with it after run returns.
	a := app{getenv: os.Getenv, stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}
	a.start = func(headless bool) error {
		return up.Command(os.Environ(), os.Getenv, os.Stdout, os.Stderr, headless)
	}
	os.Exit(a.run(os.Args[1:]))
}

// app holds what the commands need, so a test can replace the start.
type app struct {
	getenv         func(string) string
	stdin          io.Reader
	stdout, stderr io.Writer
	// start starts the sfy process, and the lead unless headless is set.
	start func(headless bool) error
}

// noFactory lists the commands that need no factory folder, so a failed factory lookup does not stop them.
var noFactory = []string{"--version", "help", "--help", "run", "hooks"}

func (a app) run(args []string) int {
	if len(args) == 0 || args[0] == "up" {
		headless := len(args) == 2 && args[1] == "--headless"
		if len(args) > 1 && !headless {
			return cli.Misuse(a.stderr, "usage: sfy up [--headless]")
		}
		// An agent that runs sfy by accident must not start a second sfy. A registered agent counts as an agent.
		if cli.Agent(a.getenv) != "" {
			cli.PrintUsage(a.stderr)
			return 1
		}
		return a.report(a.start(headless))
	}
	// The environment can be faked, so the allow rules of the settings are the real barrier. This check backs them up.
	switch args[0] {
	case "run", "serve", "register", "restart", "config", "factory-folder":
		if cli.Restricted(a.getenv) {
			return a.report(cli.ErrRestricted)
		}
	}
	// Home falls back to the default factory when the lookup fails, so a command reports a failed lookup here.
	// Only factory-folder --create makes a factory, so it skips the lookup like up does.
	makesFactory := slices.Equal(args, []string{"factory-folder", "--create"})
	if !slices.Contains(noFactory, args[0]) && !makesFactory {
		if _, err := config.Lookup(a.getenv); err != nil {
			return a.report(err)
		}
	}
	switch args[0] {
	case "--version", "help", "--help", "serve", "config":
		if len(args) > 1 {
			return cli.Misuse(a.stderr, fmt.Sprintf("%s takes no arguments", args[0]))
		}
	}
	switch args[0] {
	case "--version":
		_, _ = fmt.Fprintln(a.stdout, version)
		return 0
	case "help", "--help":
		cli.PrintUsage(a.stdout)
		return 0
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
		defer stop()
		// After the first signal the default action returns, so a second signal ends the process.
		go func() {
			<-ctx.Done()
			stop()
		}()
		return a.report(up.Serve(ctx, a.getenv, a.stderr))
	case "config":
		return a.report(up.ShowConfig(a.getenv, a.stdout, a.stderr))
	case "factory-folder":
		create := len(args) == 2 && args[1] == "--create"
		if len(args) > 1 && !create {
			return a.report(fmt.Errorf("usage: sfy factory-folder [--create]"))
		}
		return a.report(up.FactoryFolder(a.getenv, a.stdout, a.stderr, create))
	case "run":
		// On success the process becomes the command, so run returns only on an error.
		return a.report(envrun.Run(args[1:]))
	case "register":
		if len(args) > 1 {
			return cli.Misuse(a.stderr, "register takes no arguments")
		}
		// A started agent has its role already, and only a session that sfy did not start registers.
		if a.getenv("SFY_AGENT") != "" {
			cli.PrintUsage(a.stderr)
			return 1
		}
		return a.report(up.Register(os.Environ(), a.getenv, a.stdout, a.stderr))
	case "hooks":
		if len(args) != 2 || args[1] != "install" {
			return cli.Misuse(a.stderr, "usage: sfy hooks install")
		}
		if cli.Agent(a.getenv) != "" {
			cli.PrintUsage(a.stderr)
			return 1
		}
		return a.report(up.InstallHooks(a.getenv, a.stdout))
	case "restart":
		update := len(args) == 2 && args[1] == "--update"
		if len(args) > 1 && !update {
			return a.report(fmt.Errorf("usage: sfy restart [--update]"))
		}
		return a.report(up.Restart(os.Environ(), cli.WithIdentity(a.getenv), a.stdout, a.stderr, version, update))
	}
	return cli.Run(args, a.getenv, a.stdin, a.stdout, a.stderr)
}

func (a app) report(err error) int {
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "sfy: %v\n", err)
		return 1
	}
	return 0
}
