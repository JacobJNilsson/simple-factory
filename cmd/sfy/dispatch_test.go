package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDispatch(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		agent      string
		wantCode   int
		wantStart  bool
		wantStdout string
		wantStderr string
	}{
		{"operator bare sfy starts", nil, "", 0, true, "", ""},
		{"agent bare sfy prints usage", nil, "main", 1, false, "", "usage:"},
		{"help prints usage", []string{"help"}, "", 0, false, "usage:", ""},
		{"help for an agent prints usage", []string{"help"}, "worker", 0, false, "usage:", ""},
		{"--help prints usage", []string{"--help"}, "", 0, false, "usage:", ""},
		{"operator sfy up starts", []string{"up"}, "", 0, true, "", ""},
		{"agent sfy up prints usage", []string{"up"}, "main", 1, false, "", "usage:"},
		{"up takes no arguments", []string{"up", "--help"}, "", 2, false, "", "usage:"},
		{"agent sfy register prints usage", []string{"register"}, "main", 1, false, "", "usage:"},
		{"worker sfy register is refused", []string{"register"}, "worker-t1", 1, false, "", "may not run"},
		{"worker sfy run is refused", []string{"run", "-i", "env", "--", "id"}, "worker-t1", 1, false, "", "may not run"},
		{"reviewer sfy run is refused", []string{"run", "env", "--", "id"}, "review-t1-1", 1, false, "", "may not run"},
		{"worker sfy serve is refused", []string{"serve"}, "worker-t1", 1, false, "", "may not run"},
		{"reviewer sfy restart is refused", []string{"restart"}, "review-t1-1", 1, false, "", "may not run"},
		{"worker sfy hook with a free event is refused", []string{"hook", "x"}, "worker-t1", 1, false, "", "may not run"},
		{"register takes no arguments", []string{"register", "x"}, "", 2, false, "", "usage:"},
		{"agent sfy hooks install prints usage", []string{"hooks", "install"}, "main", 1, false, "", "usage:"},
		{"hooks needs install", []string{"hooks"}, "", 2, false, "", "usage:"},
		{"help takes no arguments", []string{"help", "x"}, "", 2, false, "", "usage:"},
		{"--help takes no arguments", []string{"--help", "x"}, "", 2, false, "", "usage:"},
		{"serve takes no arguments", []string{"serve", "--help"}, "", 2, false, "", "usage:"},
		{"--version takes no arguments", []string{"--version", "x"}, "", 2, false, "", "usage:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errb bytes.Buffer
			started := false
			a := app{
				getenv: func(k string) string {
					if k == "SFY_AGENT" {
						return tc.agent
					}
					return ""
				},
				stdout: &out,
				stderr: &errb,
				start:  func() error { started = true; return nil },
			}
			code := a.run(tc.args)
			if code != tc.wantCode || started != tc.wantStart {
				t.Errorf("code %d started %v, want %d %v\nstderr: %s", code, started, tc.wantCode, tc.wantStart, errb.String())
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
