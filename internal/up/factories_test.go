package up

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/protocol"
)

func TestTheMachineNamesAFactoryAsOtherFactoriesAddressIt(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	web := filepath.Join(config.FactoriesDir(home), "web")
	if err := os.MkdirAll(web, 0o700); err != nil {
		t.Fatal(err)
	}

	for folder, want := range map[string]string{web: "web", config.DefaultHome(home): "default"} {
		got, err := machine{home: folder, env: factoryTestEnv(home, folder)}.Self()
		if err != nil || got != want {
			t.Errorf("Self of %s = %q, %v, want %q", folder, got, err, want)
		}
	}
	elsewhere := filepath.Join(home, "elsewhere")
	if _, err := (machine{home: elsewhere, env: factoryTestEnv(home, elsewhere)}).Self(); err == nil {
		t.Error("a factory folder outside the factories folder got a name")
	}
}

func TestTheMachineChecksATokenAgainstTheNamedFactory(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	web := filepath.Join(config.FactoriesDir(home), "web")
	token, err := identity.Folder{Home: web}.Issue("lead")
	if err != nil {
		t.Fatal(err)
	}
	m := machine{home: config.DefaultHome(home), env: factoryTestEnv(home, "")}

	if name, err := m.Authenticate("web", protocol.Caller{Token: token}); err != nil || name != "lead" {
		t.Errorf("Authenticate = %q, %v, want lead", name, err)
	}
	var missing *noFactoryError
	if _, err := m.Authenticate("api", protocol.Caller{Token: token}); !errors.As(err, &missing) {
		t.Errorf("Authenticate for a missing factory = %v", err)
	}
	if _, err := m.Call("api", protocol.Request{Command: "status"}); !errors.As(err, &missing) {
		t.Errorf("Call to a missing factory = %v", err)
	}
}
