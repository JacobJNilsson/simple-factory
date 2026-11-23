package claude

import (
	"encoding/json"
	"fmt"
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
// The plugin's skills folder holds one link for each skill name, to the folder of the nearest layer that has it.
// A plugin directory gives the session no edit rights there, unlike a directory that --add-dir adds.
// Without a skill it removes an earlier plugin and returns no flag.
func (a adapter) pluginArgs(sessionDir string) ([]string, error) {
	dir := filepath.Join(sessionDir, "plugin")
	// RemoveAll removes the links and leaves the skills that they point at.
	if err := os.RemoveAll(dir); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	skills, err := config.Skills(a.layers)
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if len(skills) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Join(dir, ".claude-plugin"), 0o700); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	b, err := json.MarshalIndent(manifest{Name: pluginName, Description: "The skills of the sfy factory."}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude-plugin", "plugin.json"), append(b, '\n'), 0o600); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "skills"), 0o700); err != nil {
		return nil, fmt.Errorf("claude: %w", err)
	}
	// Each link keeps one copy of the skill, so an edit in a layer reaches the next session.
	for _, s := range skills {
		target, err := filepath.Abs(s.Dir)
		if err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "skills", s.Name)); err != nil {
			return nil, fmt.Errorf("claude: %w", err)
		}
	}
	return []string{"--plugin-dir", dir}, nil
}
