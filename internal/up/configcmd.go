package up

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/JacobJNilsson/simple-factory/internal/config"
	"github.com/JacobJNilsson/simple-factory/internal/prompts"
)

// promptKinds are the kinds that sfy config lists prompt files for, in the order it prints them.
var promptKinds = []string{"main", "worker", prompts.WorkerRepoPart, "reviewer"}

// hiddenValue stands in for the value of a session_settings env variable, which may hold a token.
const hiddenValue = "<set>"

// ShowConfig prints the flat result of the factory that config.Home finds: the settings with the file behind each key,
// the prompt files of each kind in append order, and the factory files. It reads files only and creates no factory.
func ShowConfig(env func(string) string, stdout io.Writer) error {
	home, userHome := config.Home(env), env("HOME")
	cfg, err := config.LoadFactory(home, userHome)
	if err != nil {
		return err
	}
	sources, err := config.Sources(home, userHome)
	if err != nil {
		return err
	}
	layers := config.Layers(home, userHome)
	var out strings.Builder
	fmt.Fprintf(&out, "# %s\n# Layers, lowest first: %s\n\n# Settings\n", config.Header(home, env), strings.Join(layers, ", "))
	if err := writeSettings(&out, cfg, sources); err != nil {
		return err
	}
	out.WriteString("\n# Prompts\n")
	for _, kind := range promptKinds {
		if err := writePrompts(&out, kind, layers); err != nil {
			return err
		}
	}
	files, err := config.FactoryFiles(home, userHome)
	if err != nil {
		return err
	}
	out.WriteString("\n# Factory files\n")
	for _, name := range files {
		fmt.Fprintf(&out, "# %s\n", name)
	}
	_, err = io.WriteString(stdout, out.String())
	return err
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
