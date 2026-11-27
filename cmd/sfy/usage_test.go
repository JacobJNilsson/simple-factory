package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/cli"
)

func TestUsageListsEveryHandParsedFlag(t *testing.T) {
	var b bytes.Buffer
	cli.PrintUsage(&b)
	text := b.String()
	for _, h := range handFlags {
		var line string
		for _, l := range strings.Split(text, "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "sfy "+h.command+" ") {
				line = l
				break
			}
		}
		if h.unlisted {
			if line != "" {
				t.Errorf("the usage lists %q, which it leaves out on purpose", line)
			}
			continue
		}
		if !strings.Contains(line, "["+h.flag) {
			t.Errorf("the usage line %q of sfy %s lacks [%s", line, h.command, h.flag)
		}
	}
}
