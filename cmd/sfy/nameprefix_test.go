package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestACommandFailsOnABadNamePrefixButHelpAndVersionDoNot(t *testing.T) {
	env := func(k string) string {
		if k == "SFY_NAME_PREFIX" {
			return "EX"
		}
		return ""
	}
	for _, c := range []struct {
		args []string
		code int
	}{{[]string{"status"}, 1}, {[]string{"config"}, 1}, {[]string{"help"}, 0}, {[]string{"--version"}, 0}} {
		var out, errb bytes.Buffer
		a := app{getenv: env, stdout: &out, stderr: &errb}

		code := a.run(c.args)

		if code != c.code {
			t.Errorf("%v: code %d, want %d", c.args, code, c.code)
		}
		if c.code == 1 && !strings.Contains(errb.String(), "SFY_NAME_PREFIX") {
			t.Errorf("%v: stderr %q, want it to name SFY_NAME_PREFIX", c.args, errb.String())
		}
	}
}
