package config_test

import (
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

func TestPrefixIsEmptyForTheDefaultFolderAndNamesOtherFolders(t *testing.T) {
	cases := []struct{ home, want string }{
		{"/h/.sfy", ""},
		{"/h/.sfy/", ""},
		{"/h/.sfy-work", "sfy-work-"},
		{"/h/Work Two", "work-two-"},
		{"/h/team_A.b", "team-a-b-"},
		{"/other/.sfy", "sfy-"},
		{"/h/sfy", "sfy-"},
	}
	env := func(k string) string {
		if k == "HOME" {
			return "/h"
		}
		return ""
	}
	for _, c := range cases {
		if got := config.Prefix(c.home, env); got != c.want {
			t.Errorf("Prefix(%q) = %q, want %q", c.home, got, c.want)
		}
	}
}
