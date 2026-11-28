// Command sfy runs coding agents as interactive sessions in terminal panes.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
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
	a.start = func(mode up.Mode) error {
		return up.Command(os.Environ(), os.Getenv, os.Stdout, os.Stderr, mode)
	}
	os.Exit(a.run(os.Args[1:]))
}

// app holds what the commands need, so a test can replace the start.
type app struct {
	getenv         func(string) string
	stdin          io.Reader
	stdout, stderr io.Writer
	// start starts the sfy process, and the lead unless the mode says headless.
	start func(mode up.Mode) error
}

// handFlag is a flag that run parses by hand, outside the forms of package cli.
type handFlag struct {
	command, flag string
	// unlisted marks a command that the usage leaves out on purpose.
	unlisted bool
}

// handFlags lists every flag that run parses by hand, so a test checks that the usage names each one.
var handFlags = []handFlag{
	{command: "up", flag: "--headless"},
	{command: "up", flag: "--new-factory"},
	{command: "restart", flag: "--update"},
	{command: "restart", flag: "--headless"},
	{command: "factory-folder", flag: "--create", unlisted: true},
	{command: "factory-folder", flag: "--check", unlisted: true},
	{command: "factory-folder", flag: "--exact", unlisted: true},
}

// folderModes maps the arguments of sfy factory-folder to its mode.
var folderModes = map[string]up.FolderMode{
	"": up.FindFolder, "--create": up.CreateFolder, "--check": up.CheckFolder,
	"--create --exact": up.CreateExactFolder, "--exact --create": up.CreateExactFolder,
	"--check --exact": up.CheckExactFolder, "--exact --check": up.CheckExactFolder,
}

// folderUsage is the usage line of sfy factory-folder, which the usage of sfy leaves out.
const folderUsage = "usage: sfy factory-folder [--create | --check] [--exact]"

// noFactory lists the commands that need no factory folder, so a failed factory lookup does not stop them.
var noFactory = []string{"--version", "help", "--help", "run", "hooks", "factories"}

func (a app) run(args []string) int {
	if len(args) == 0 || args[0] == "up" {
		var flags []string
		if len(args) > 1 {
			flags = args[1:]
		}
		set, ok := flagSet(flags, "--headless", "--new-factory")
		if !ok {
			return cli.Misuse(a.stderr, "usage: sfy up [--headless] [--new-factory]")
		}
		// An agent that runs sfy by accident must not start a second sfy. A registered agent counts as an agent.
		if cli.Agent(a.getenv) != "" {
			cli.PrintUsage(a.stderr)
			return 1
		}
		return a.report(a.start(up.Mode{Headless: set["--headless"], NewFactory: set["--new-factory"]}))
	}
	// The environment can be faked, so the allow rules of the settings are the real barrier. This check backs them up.
	switch args[0] {
	case "run", "serve", "register", "restart", "config", "factory-folder", "label", "factories":
		if cli.Restricted(a.getenv) {
			return a.report(cli.ErrRestricted)
		}
	}
	// Home falls back to the default factory when the lookup fails, so a command reports a failed lookup here.
	// Only factory-folder --create makes a factory, so it skips the lookup like up does. factory-folder --check reports a failed lookup itself.
	// sfy restart looks up its factory itself, so a failed lookup can name the factories of other name prefixes.
	mode, known := folderModes[strings.Join(args[1:], " ")]
	ownLookup := args[0] == "factory-folder" && known && mode != up.FindFolder
	if !slices.Contains(noFactory, args[0]) && !ownLookup && args[0] != "restart" {
		if _, err := config.Lookup(a.getenv); err != nil {
			return a.report(err)
		}
	}
	switch args[0] {
	case "--version", "help", "--help", "serve", "config", "factories":
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
	case "factories":
		return a.report(up.Factories(a.getenv, a.stdout))
	case "label":
		err := up.Label(a.getenv, a.stdout, strings.Join(args[1:], " "))
		var invalid *up.InvalidLabelError
		if errors.As(err, &invalid) {
			return cli.Misuse(a.stderr, err.Error())
		}
		return a.report(err)
	case "factory-folder":
		if !known {
			return a.report(errors.New(folderUsage))
		}
		err := up.FactoryFolder(a.getenv, a.stdout, a.stderr, mode)
		if errors.Is(err, up.ErrNoFactory) {
			return cli.ExitNoFactory
		}
		return a.report(err)
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
		set, ok := flagSet(args[1:], "--update", "--headless")
		if !ok {
			return a.report(fmt.Errorf("usage: sfy restart [--update] [--headless]"))
		}
		return a.report(up.Restart(os.Environ(), cli.WithIdentity(a.getenv), a.stdout, a.stderr, version, set["--update"], set["--headless"]))
	}
	return cli.Run(args, a.getenv, a.stdin, a.stdout, a.stderr)
}

// flagSet reads args as flags from names, in any order. It reports false for any other word and for a repeated flag.
func flagSet(args []string, names ...string) (map[string]bool, bool) {
	set := map[string]bool{}
	for _, arg := range args {
		if !slices.Contains(names, arg) || set[arg] {
			return nil, false
		}
		set[arg] = true
	}
	return set, true
}

func (a app) report(err error) int {
	if err != nil {
		_, _ = fmt.Fprintf(a.stderr, "sfy: %v\n", err)
		return 1
	}
	return 0
}
