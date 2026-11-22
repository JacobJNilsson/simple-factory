package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/JacobJNilsson/simple-factory/internal/config"
)

// pluginName is the plugin's name in its manifest. Claude Code shows each skill as factory:<skill>.
const pluginName = "factory"

// manifest is the plugin manifest that Claude Code reads from .claude-plugin/plugin.json.
type manifest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// pluginArgs writes the plugin of the factory's skills into sessionDir and returns the flag that loads it.
// A plugin directory gives the session no edit rights there, unlike a directory that --add-dir adds.
// Without a skills folder it removes an earlier plugin and returns no flag.
func (a adapter) pluginArgs(sessionDir string) ([]string, error) {
	dir := filepath.Join(sessionDir, "plugin")
	// RemoveAll removes the link and leaves the skills that it points at.
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	skills, err := filepath.Abs(config.SkillsDir(a.home))
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	st, err := os.Stat(skills)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !st.IsDir() {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	b, err := json.MarshalIndent(manifest{Name: pluginName, Description: "The skills of the sfy factory folder."}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), append(b, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	// The link keeps one copy of the skills, so an edit in the factory folder reaches the next session.
	if err := os.Symlink(skills, filepath.Join(dir, "skills")); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	return []string{"--plugin-dir", dir}, nil
}
