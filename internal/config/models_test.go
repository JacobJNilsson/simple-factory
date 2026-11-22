package config_test

import (
	"errors"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

const auditor = `
[agents.auditor]
harness = "claude"
ends = "verdict"
session = "round"
`

func TestModelsTakeAKeyForEachClaudeAgent(t *testing.T) {
	home, userHome := writeFactory(t, auditor, "[models]\nauditor = \"opus\"\nworker = \"haiku\"\n")
	cfg, _, err := config.LoadProcess(home, userHome)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.ModelOf("auditor", true); got != "opus" {
		t.Errorf("model of auditor = %q, want opus", got)
	}
	if got := cfg.ModelOf("worker", false); got != "haiku" {
		t.Errorf("model of worker = %q, want haiku", got)
	}
	if got := cfg.ModelOf("push", false); got != "haiku" {
		t.Errorf("model of push = %q, want the worker's haiku", got)
	}
}

func TestModelsRefuseAKeyThatNamesNoClaudeAgent(t *testing.T) {
	home, userHome := writeFactory(t, "", "[models]\ncheck = \"opus\"\n")
	_, _, err := config.LoadProcess(home, userHome)
	var setting *config.SettingError
	if !errors.As(err, &setting) || setting.Setting != "models.check" {
		t.Fatalf("LoadProcess = %v, want an unknown setting models.check", err)
	}
}
