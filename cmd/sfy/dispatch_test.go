package main

import (
	"bytes"
	"strings"
	"testing"
)

// starts names the start that a case expects: none, a start with the lead, or a headless start.
type starts int

const (
	noStart starts = iota
	leadStart
	headlessStart
)

func TestDispatch(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		agent      string
		wantCode   int
		wantStart  starts
		wantStdout string
		wantStderr string
	}{
		{"operator bare sfy starts", nil, "", 0, leadStart, "", ""},
		{"agent bare sfy prints usage", nil, "lead", 1, noStart, "", "usage:"},
		{"help prints usage", []string{"help"}, "", 0, noStart, "--headless", ""},
		{"help for an agent prints usage", []string{"help"}, "worker", 0, noStart, "usage:", ""},
		{"--help prints usage", []string{"--help"}, "", 0, noStart, "usage:", ""},
		{"operator sfy up starts with a lead", []string{"up"}, "", 0, leadStart, "", ""},
		{"operator sfy up --headless starts headless", []string{"up", "--headless"}, "", 0, headlessStart, "", ""},
		{"agent sfy up prints usage", []string{"up"}, "lead", 1, noStart, "", "usage:"},
		{"agent sfy up --headless prints usage", []string{"up", "--headless"}, "lead", 1, noStart, "", "usage:"},
		{"up takes only --headless", []string{"up", "--help"}, "", 2, noStart, "", "usage: sfy up [--headless]"},
		{"up takes --headless alone", []string{"up", "--headless", "x"}, "", 2, noStart, "", "usage: sfy up [--headless]"},
		{"bare --headless is no command", []string{"--headless"}, "", 2, noStart, "", "usage:"},
		{"agent sfy register prints usage", []string{"register"}, "lead", 1, noStart, "", "usage:"},
		{"worker sfy register is refused", []string{"register"}, "worker-t1", 1, noStart, "", "may not run"},
		{"worker sfy run is refused", []string{"run", "-i", "env", "--", "id"}, "worker-t1", 1, noStart, "", "may not run"},
		{"reviewer sfy run is refused", []string{"run", "env", "--", "id"}, "review-t1-1", 1, noStart, "", "may not run"},
		{"worker sfy serve is refused", []string{"serve"}, "worker-t1", 1, noStart, "", "may not run"},
		{"reviewer sfy restart is refused", []string{"restart"}, "review-t1-1", 1, noStart, "", "may not run"},
		{"worker sfy config is refused", []string{"config"}, "worker-t1", 1, noStart, "", "may not run"},
		{"worker sfy factory-folder is refused", []string{"factory-folder"}, "worker-t1", 1, noStart, "", "may not run"},
		{"reviewer sfy factory-folder --create is refused", []string{"factory-folder", "--create"}, "review-t1-1", 1, noStart, "", "may not run"},
		{"factory-folder takes only --create", []string{"factory-folder", "x"}, "", 1, noStart, "", "usage: sfy factory-folder"},
		{"config takes no arguments", []string{"config", "x"}, "", 2, noStart, "", "usage:"},
		{"worker sfy hook with a free event is refused", []string{"hook", "x"}, "worker-t1", 1, noStart, "", "may not run"},
		{"register takes no arguments", []string{"register", "x"}, "", 2, noStart, "", "usage:"},
		{"agent sfy hooks install prints usage", []string{"hooks", "install"}, "lead", 1, noStart, "", "usage:"},
		{"hooks needs install", []string{"hooks"}, "", 2, noStart, "", "usage:"},
		{"help takes no arguments", []string{"help", "x"}, "", 2, noStart, "", "usage:"},
		{"--help takes no arguments", []string{"--help", "x"}, "", 2, noStart, "", "usage:"},
		{"serve takes no arguments", []string{"serve", "--help"}, "", 2, noStart, "", "usage:"},
		{"--version takes no arguments", []string{"--version", "x"}, "", 2, noStart, "", "usage:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			started := noStart
			a := app{
				getenv: func(k string) string {
					if k == "SFY_AGENT" {
						return tc.agent
					}
					return ""
				},
				stdout: &out,
				stderr: &errb,
				start: func(headless bool) error {
					started = leadStart
					if headless {
						started = headlessStart
					}
					return nil
				},
			}
			code := a.run(tc.args)
			if code != tc.wantCode || started != tc.wantStart {
				t.Errorf("code %d start %d, want %d %d\nstderr: %s", code, started, tc.wantCode, tc.wantStart, errb.String())
			}
			if !strings.Contains(out.String(), tc.wantStdout) || !strings.Contains(errb.String(), tc.wantStderr) {
				t.Errorf("stdout %q stderr %q, want %q and %q", out.String(), errb.String(), tc.wantStdout, tc.wantStderr)
			}
			if tc.wantStdout == "" && out.Len() > 0 {
				t.Errorf("unexpected stdout %q", out.String())
			}
		})
	}
}
