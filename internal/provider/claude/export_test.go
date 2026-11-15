package claude

import "github.com/JacobJNilsson/simple-factory/internal/config"

// NewForOS returns the adapter as if it ran on goos.
func NewForOS(cfg config.Config, home, sfyBin, goos string, uid int) adapter {
	return adapter{cfg: cfg, home: home, sfyBin: sfyBin, goos: goos, uid: uid}
}
