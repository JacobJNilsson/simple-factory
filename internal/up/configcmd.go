package up

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/identity"
	"github.com/JacobJNilsson/simple-factory/internal/process"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

// promptKinds are the kinds that sfy config lists prompt files for, in the order it prints them.
var promptKinds = []string{"lead", prompts.LeadMemoryPart, "worker", prompts.WorkerRepoPart, "reviewer"}

// hiddenValue stands in for the value of a session_settings env variable, which may hold a token.
const hiddenValue = "<set>"

// ShowConfig prints the flat result of the factory that config.Home finds: the settings with the file behind each key,
// the prompt files of each kind in append order, the skills, the factory files, the sessions with a token in the environment, and the old names.
// It reads files only and creates no factory. Each listed session and each old name also gets a warning line on stderr.
func ShowConfig(env func(string) string, stdout, stderr io.Writer) error {
	home, userHome := config.Home(env), env("HOME")
	cfg, proc, err := loadDefinition(home, userHome)
	if err != nil {
		return err
	}
	sources, err := config.Sources(home, userHome)
	if err != nil {
		return err
	}
	layers, err := config.Layers(home, userHome)
	if err != nil {
		return err
	}
	skipped, err := config.Skipped(home, userHome)
	if err != nil {
		return err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n", config.Header(home, env))
	writeLayers(&out, layers, skipped)
	out.WriteString("\n# Settings\n")
	if err := writeSettings(&out, cfg, sources); err != nil {
		return err
	}
	writeProcess(&out, proc, sources)
	out.WriteString("\n# Prompts\n")
	for _, kind := range append(slices.Clone(promptKinds), agentPrompts(proc)...) {
		if err := writePrompts(&out, kind, layers); err != nil {
			return err
		}
	}
	skills, err := config.Skills(layers)
	if err != nil {
		return err
	}
	out.WriteString("\n# Skills\n")
	for _, s := range skills {
		fmt.Fprintf(&out, "# %s: %s\n", s.Name, s.Dir)
	}
	files, err := config.FactoryFiles(home, userHome)
	if err != nil {
		return err
	}
	out.WriteString("\n# Factory files\n")
	for _, name := range files {
		fmt.Fprintf(&out, "# %s\n", name)
	}
	old, err := config.OldNames(home, userHome)
	if err != nil {
		return err
	}
	tokens, err := envTokenLines(home, sessionAlive(env))
	if err != nil {
		return err
	}
	// A later release stops reading SFY_TOKEN, and it waits until no factory lists a session here.
	out.WriteString("\n# Sessions with a token in the environment\n")
	for _, line := range tokens {
		fmt.Fprintf(&out, "# %s\n", line)
	}
	// A later release removes the old names, and it waits until no factory lists one here.
	out.WriteString("\n# Old names\n")
	for _, line := range old {
		fmt.Fprintf(&out, "# %s\n", line)
	}
	if _, err = io.WriteString(stdout, out.String()); err != nil {
		return err
	}
	for _, line := range append(tokens, old...) {
		if _, err := fmt.Fprintln(stderr, "sfy: warning: "+line); err != nil {
			return err
		}
	}
	return nil
}

// sessionAlive returns the alive check of the launcher that the engine uses. It builds the launcher at the first check.
// When sfy cannot build the launcher, every check fails with that error.
func sessionAlive(env func(string) string) func(name string) (bool, error) {
	build := sync.OnceValues(func() (parts, error) { return newParts(env) })
	return func(name string) (bool, error) {
		p, err := build()
		if err != nil {
			return false, err
		}
		return p.launcher.Alive(name)
	}
}

// envTokenLines returns one line for each running session that an older sfy started with its token in SFY_TOKEN.
// A session whose alive check fails gets a line too, because it may still run.
func envTokenLines(home string, alive func(name string) (bool, error)) ([]string, error) {
	names, err := identity.Folder{Home: home}.EnvTokenSessions()
	if err != nil {
		return nil, err
	}
	var lines []string
	for _, name := range names {
		runs, err := alive(name)
		switch {
		case err != nil:
			lines = append(lines, fmt.Sprintf("Session %s has a token file and no process record (sfy cannot tell whether it runs). An older sfy started it with its token in SFY_TOKEN. Restart it if it still runs.", name))
		case runs:
			lines = append(lines, fmt.Sprintf("Session %s runs with a token file and no process record. An older sfy started it with its token in SFY_TOKEN. Restart it.", name))
		}
	}
	return lines, nil
}

// writeLayers writes the layers, lowest first, each with its role, and then each skipped override with its reason.
// The first folder is the factory folder, and the others are overrides.
func writeLayers(out *strings.Builder, layers []string, skipped []config.Override) {
	out.WriteString("# Layers, lowest first:\n# - built-in defaults\n")
	for i, dir := range layers {
		role := "override"
		if i == 0 {
			role = "factory folder"
		}
		fmt.Fprintf(out, "# - %s (%s)\n", dir, role)
	}
	if len(skipped) == 0 {
		return
	}
	out.WriteString("# Skipped overrides, which sfy does not read:\n")
	for _, o := range skipped {
		fmt.Fprintf(out, "# - %s: %s\n", o.Dir, o.Skip)
	}
}

// warnSkipped prints one warning line for each override that sfy skips. A failure is a warning too and never stops sfy.
func warnSkipped(warn io.Writer, home, userHome string) {
	skipped, err := config.Skipped(home, userHome)
	if err != nil {
		_, _ = fmt.Fprintln(warn, "sfy: warning: no check of overrides: "+err.Error())
	}
	for _, o := range skipped {
		_, _ = fmt.Fprintf(warn, "sfy: warning: skipped the override %s: %s\n", o.Dir, o.Skip)
	}
}

// writeProcess writes the task process: the start list, each list with its joined agents and branches, and each agent.
// Each list names the file that set its agents last, because a later file replaces a list's agents as a whole.
// An agent that runs outside the sandbox or with its own path lists is marked wide.
func writeProcess(out *strings.Builder, p process.Process, sources map[string]string) {
	source := func(key string) string { return cmp.Or(sources[key], "default") }
	fmt.Fprintf(out, "\n# Process\n# start = %s # %s\n", p.Start, source("start"))
	for _, name := range slices.Sorted(maps.Keys(p.Lists)) {
		l := p.Lists[name]
		agents, _ := p.Flatten(name)
		fmt.Fprintf(out, "# list %s = %s # %s\n", name, strings.Join(agents, ", "), source("lists."+name+".agents"))
		fmt.Fprintf(out, "#   pass: %s\n", passText(l.Then.Pass))
		var fails []string
		for _, agent := range slices.Sorted(maps.Keys(l.Then.FailBy)) {
			fails = append(fails, fmt.Sprintf("at %s %s", agent, targetText(l.Then.FailBy[agent])))
		}
		fails = append(fails, "otherwise "+targetText(l.Then.Fail))
		fmt.Fprintf(out, "#   fail: %s\n", strings.Join(fails, ", "))
	}
	for _, name := range slices.Sorted(maps.Keys(p.Agents)) {
		a := p.Agents[name]
		line := fmt.Sprintf("# agent %s: harness %s, ends %s", name, a.Harness, a.Ends)
		if a.Harness == process.Shell {
			line += ", run " + strings.Join(a.Run, " && ")
		}
		if a.Like != "" {
			line += ", like " + a.Like
		}
		if a.Harness == process.Shell && a.Permissions.Wide() {
			line += ", wide"
			if !a.Permissions.Sandbox {
				line += ": no sandbox"
			}
		}
		out.WriteString(line + "\n")
	}
}

func targetText(t process.Target) string {
	if t.Round {
		return t.List + " with a new round"
	}
	return t.List
}

func passText(b process.Branch) string {
	switch {
	case b.Target != nil:
		return targetText(*b.Target)
	case b.Shell != nil:
		return fmt.Sprintf("the last line of %s, one of %s", b.Shell.Agent, strings.Join(b.Shell.To, ", "))
	}
	var parts []string
	for _, c := range b.Cases {
		if len(c.If) == 0 {
			parts = append(parts, "otherwise "+targetText(c.Target))
			continue
		}
		parts = append(parts, fmt.Sprintf("if %s %s", strings.Join(c.If, " and "), targetText(c.Target)))
	}
	return strings.Join(parts, ", ")
}

// writeSettings writes each setting of cfg as a TOML line with a dotted key, followed by the file that set it last or default.
func writeSettings(out *strings.Builder, cfg config.Config, sources map[string]string) error {
	flat, err := flatSettings(cfg)
	if err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(flat)) {
		source, ok := sources[key]
		if !ok {
			source = "default"
		}
		fmt.Fprintf(out, "%s = %s # %s\n", key, flat[key], source)
	}
	return nil
}

// flatSettings maps the dotted key of each setting of cfg to its inline TOML value, with each session_settings env value hidden.
func flatSettings(cfg config.Config) (map[string]string, error) {
	var text bytes.Buffer
	if err := toml.NewEncoder(&text).Encode(cfg); err != nil {
		return nil, err
	}
	var raw decodedTable
	if _, err := toml.Decode(text.String(), &raw); err != nil {
		return nil, err
	}
	flat := map[string]string{}
	if err := flatten(raw, nil, flat); err != nil {
		return nil, err
	}
	return flat, nil
}

// decodedTable is a TOML table as toml.Decode gives it. A nested table is a map[string]any.
type decodedTable map[string]any

// flatten adds each value below table that is not a table to flat, keyed by its dotted key and written as an inline TOML value.
func flatten(table decodedTable, prefix toml.Key, flat map[string]string) error {
	for k, v := range table {
		key := append(append(toml.Key{}, prefix...), k)
		if sub, ok := v.(map[string]any); ok {
			if err := flatten(sub, key, flat); err != nil {
				return err
			}
			continue
		}
		if secret(key) {
			flat[key.String()] = `"` + hiddenValue + `"`
			continue
		}
		text, err := inline(v)
		if err != nil {
			return err
		}
		flat[key.String()] = text
	}
	return nil
}

// secret reports whether key names a variable of a session_settings env table.
func secret(key toml.Key) bool {
	return len(key) == 4 && key[0] == "session_settings" && key[2] == "env"
}

// inline writes one decoded value as an inline TOML value.
// CONTRACT: toml.Decode gives each value of a table as any, so v is any.
func inline(v any) (string, error) {
	if s, ok := v.(string); ok {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		// JSON escapes are TOML basic string escapes too.
		if err := enc.Encode(s); err != nil {
			return "", err
		}
		return strings.TrimSpace(b.String()), nil
	}
	if t, ok := v.(time.Time); ok {
		return t.Format(time.RFC3339Nano), nil
	}
	if f, ok := v.(float64); ok {
		text := strconv.FormatFloat(f, 'g', -1, 64)
		if !strings.ContainsAny(text, ".eIN") {
			text += ".0"
		}
		return text, nil
	}
	if tables, ok := v.([]map[string]any); ok {
		items := make([]any, 0, len(tables))
		for _, item := range tables {
			items = append(items, item)
		}
		return inline(items)
	}
	if items, ok := v.([]any); ok {
		parts := make([]string, 0, len(items))
		for _, item := range items {
			text, err := inline(item)
			if err != nil {
				return "", err
			}
			parts = append(parts, text)
		}
		return "[" + strings.Join(parts, ", ") + "]", nil
	}
	if table, ok := v.(map[string]any); ok {
		parts := make([]string, 0, len(table))
		for _, k := range slices.Sorted(maps.Keys(table)) {
			text, err := inline(table[k])
			if err != nil {
				return "", err
			}
			parts = append(parts, toml.Key{k}.String()+" = "+text)
		}
		return "{ " + strings.Join(parts, ", ") + " }", nil
	}
	return fmt.Sprint(v), nil
}

// writePrompts writes the base file of a kind and each addition in append order. A removed addition names the file that removed it.
func writePrompts(out *strings.Builder, kind string, layers []string) error {
	base, additions, err := prompts.Sources(kind, layers)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "# %s: base %s\n", kind, base)
	for _, a := range additions {
		if a.Removed {
			fmt.Fprintf(out, "#   %s: removed by %s\n", a.Name, a.File)
			continue
		}
		fmt.Fprintf(out, "#   %s: %s\n", a.Name, a.File)
	}
	return nil
}
