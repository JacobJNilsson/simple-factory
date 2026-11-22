package process

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// rawTable is a TOML table as the decoder gives it. A nested table is a map[string]any.
type rawTable map[string]any

// merge copies over onto base key by key at every depth. A value that is not a table, a list too, replaces the value below it.
func merge(base, over rawTable) rawTable {
	out := maps.Clone(base)
	for k, v := range over {
		sub, isTable := v.(map[string]any)
		below, wasTable := out[k].(map[string]any)
		if isTable && wasTable {
			out[k] = map[string]any(merge(below, sub))
			continue
		}
		out[k] = v
	}
	return out
}

// tables holds the three keys of a config text that describe the process.
type tables struct {
	Start  string                    `toml:"start"`
	Agents map[string]agentTable     `toml:"agents"`
	Lists  map[string]toml.Primitive `toml:"lists"`
}

type agentTable struct {
	Harness     string          `toml:"harness"`
	Prompt      string          `toml:"prompt"`
	RepoPrompt  string          `toml:"repo_prompt"`
	Model       string          `toml:"model"`
	Session     string          `toml:"session"`
	SessionName string          `toml:"session_name"`
	Like        string          `toml:"like"`
	Run         []string        `toml:"run"`
	Ends        string          `toml:"ends"`
	CountRound  bool            `toml:"count_round"`
	Feedback    string          `toml:"feedback"`
	Slot        bool            `toml:"slot"`
	Show        string          `toml:"show"`
	Message     string          `toml:"message"`
	Receives    string          `toml:"receives"`
	Permissions permissionTable `toml:"permissions"`
}

type permissionTable struct {
	Sandbox          *bool     `toml:"sandbox"`
	Edit             *bool     `toml:"edit"`
	Push             bool      `toml:"push"`
	Allow            []string  `toml:"allow"`
	ExcludedCommands []string  `toml:"excluded_commands"`
	AllowWrite       *[]string `toml:"allow_write"`
	DenyRead         *[]string `toml:"deny_read"`
	PassEnv          *[]string `toml:"pass_env"`
}

type listTable struct {
	Agents    []string `toml:"agents"`
	MaxRounds int      `toml:"max_rounds"`
	Then      struct {
		Pass toml.Primitive `toml:"pass"`
		Fail toml.Primitive `toml:"fail"`
	} `toml:"then"`
}

type targetTable struct {
	List  string `toml:"list"`
	Round bool   `toml:"round"`
}

type caseTable struct {
	If    toml.Primitive `toml:"if"`
	List  string         `toml:"list"`
	Round bool           `toml:"round"`
}

type shellTable struct {
	Agent string   `toml:"agent"`
	To    []string `toml:"to"`
}

// DefaultText returns the TOML text of the embedded default process.
func DefaultText() string { return defaultTOML }

// Default returns the embedded default process with claude as the default harness.
func Default() Process {
	p, err := Parse("claude")
	if err != nil {
		// The default is part of the binary, so a default that does not load is a build fault that no caller can handle.
		panic(fmt.Sprintf("process: the embedded default does not load: %v", err))
	}
	return p
}

// Parse merges the agents, lists and start keys of each TOML text onto the embedded default, in order, and decodes the result.
// Other keys of a text are ignored. provider is the harness of an agent that names none and copies no other agent.
func Parse(provider string, texts ...string) (Process, error) {
	merged := rawTable{}
	for _, text := range append([]string{defaultTOML}, texts...) {
		raw := rawTable{}
		if _, err := toml.Decode(text, (*map[string]any)(&raw)); err != nil {
			return Process{}, fmt.Errorf("process: %w", err)
		}
		own := rawTable{}
		for _, k := range []string{"start", "agents", "lists"} {
			if v, ok := raw[k]; ok {
				own[k] = v
			}
		}
		merged = merge(merged, own)
	}
	var text strings.Builder
	if err := toml.NewEncoder(&text).Encode(map[string]any(merged)); err != nil {
		return Process{}, fmt.Errorf("process: %w", err)
	}
	return decode(text.String(), provider)
}

func decode(text, provider string) (Process, error) {
	var t tables
	md, err := toml.Decode(text, &t)
	if err != nil {
		return Process{}, fmt.Errorf("process: %w", err)
	}
	p := Process{Start: t.Start, Agents: map[string]Agent{}, Lists: map[string]List{}}
	for name, a := range t.Agents {
		p.Agents[name] = a.agent(name, keysBelow(md, "agents", name), keysBelow(md, "agents", name, "permissions"))
	}
	for name, prim := range t.Lists {
		l, err := decodeList(md, name, prim)
		if err != nil {
			return Process{}, err
		}
		p.Lists[name] = l
	}
	for _, key := range md.Undecoded() {
		if len(key) > 1 && key[0] == "agents" {
			return Process{}, fmt.Errorf("process: unknown key %s", key)
		}
	}
	if err := p.resolve(provider); err != nil {
		return Process{}, err
	}
	return p, nil
}

// keysBelow returns the sorted names of the keys directly below the key path.
func keysBelow(md toml.MetaData, path ...string) []string {
	var out []string
	for _, k := range md.Keys() {
		if len(k) == len(path)+1 && slices.Equal(k[:len(path)], path) {
			out = append(out, k[len(path)])
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (a agentTable) agent(name string, keys, permissionKeys []string) Agent {
	pt := a.Permissions
	return Agent{
		Name: name, Harness: a.Harness, Prompt: a.Prompt, RepoPrompt: a.RepoPrompt, Model: a.Model,
		Session: a.Session, SessionName: a.SessionName, Like: a.Like, Run: a.Run, Ends: a.Ends,
		CountRound: a.CountRound, Feedback: a.Feedback, Slot: a.Slot, Show: a.Show, Message: a.Message, Receives: a.Receives,
		Permissions: Permissions{
			Sandbox: pt.Sandbox == nil || *pt.Sandbox, Edit: pt.Edit == nil || *pt.Edit, Push: pt.Push,
			Allow: pt.Allow, ExcludedCommands: pt.ExcludedCommands, AllowWrite: pt.AllowWrite, DenyRead: pt.DenyRead, PassEnv: pt.PassEnv,
			Keys: permissionKeys,
		},
		Keys: keys,
	}
}

func decodeList(md toml.MetaData, name string, prim toml.Primitive) (List, error) {
	var lt listTable
	if err := md.PrimitiveDecode(prim, &lt); err != nil {
		return List{}, fmt.Errorf("process: lists.%s: %w", name, err)
	}
	for _, k := range keysBelow(md, "lists", name) {
		if k != "agents" && k != "max_rounds" && k != "then" {
			return List{}, fmt.Errorf("process: lists.%s has the unknown key %s", name, k)
		}
	}
	for _, k := range keysBelow(md, "lists", name, "then") {
		if k != "pass" && k != "fail" {
			return List{}, fmt.Errorf("process: lists.%s.then has the unknown key %s", name, k)
		}
	}
	if md.IsDefined("lists", name, "max_rounds") && lt.MaxRounds < 1 {
		return List{}, fmt.Errorf("process: lists.%s.max_rounds must be 1 or more", name)
	}
	l := List{Name: name, Agents: lt.Agents, MaxRounds: lt.MaxRounds}
	where := "lists." + name + ".then"
	l.Then.Pass.Target = &Target{List: Done}
	if md.IsDefined("lists", name, "then", "pass") {
		b, err := decodePass(md, where+".pass", lt.Then.Pass)
		if err != nil {
			return List{}, err
		}
		l.Then.Pass = b
	}
	l.Then.Fail = Target{List: Escalated}
	if md.IsDefined("lists", name, "then", "fail") {
		if err := decodeFail(md, where+".fail", lt.Then.Fail, &l.Then); err != nil {
			return List{}, err
		}
	}
	return l, nil
}

// decodeTarget reads a list name or a target table.
func decodeTarget(md toml.MetaData, where string, prim toml.Primitive) (Target, error) {
	var name string
	if err := md.PrimitiveDecode(prim, &name); err == nil {
		return Target{List: name}, nil
	}
	var tt targetTable
	if err := md.PrimitiveDecode(prim, &tt); err != nil || tt.List == "" {
		return Target{}, fmt.Errorf("process: %s must be a list name or a table with a list key", where)
	}
	return Target(tt), nil
}

func decodePass(md toml.MetaData, where string, prim toml.Primitive) (Branch, error) {
	var cases []caseTable
	if err := md.PrimitiveDecode(prim, &cases); err == nil {
		b := Branch{Cases: []Case{}}
		for i, c := range cases {
			if c.List == "" {
				return Branch{}, fmt.Errorf("process: %s entry %d names no list", where, i+1)
			}
			facts, err := stringOrList(md, c.If)
			if err != nil {
				return Branch{}, fmt.Errorf("process: %s entry %d: if must be a fact or a list of facts", where, i+1)
			}
			b.Cases = append(b.Cases, Case{If: facts, Target: Target{List: c.List, Round: c.Round}})
		}
		return b, nil
	}
	var shell shellTable
	if err := md.PrimitiveDecode(prim, &shell); err == nil && shell.Agent != "" {
		return Branch{Shell: &ShellBranch{Agent: shell.Agent, To: shell.To}}, nil
	}
	t, err := decodeTarget(md, where, prim)
	if err != nil {
		return Branch{}, err
	}
	return Branch{Target: &t}, nil
}

// decodeFail reads a target, or a table of targets keyed by the name of the agent that failed.
func decodeFail(md toml.MetaData, where string, prim toml.Primitive, then *Then) error {
	var byAgent map[string]toml.Primitive
	if err := md.PrimitiveDecode(prim, &byAgent); err == nil {
		if _, isTarget := byAgent["list"]; !isTarget {
			then.FailBy = map[string]Target{}
			for agent, p := range byAgent {
				t, err := decodeTarget(md, where+"."+agent, p)
				if err != nil {
					return err
				}
				then.FailBy[agent] = t
			}
			return nil
		}
	}
	t, err := decodeTarget(md, where, prim)
	if err != nil {
		return err
	}
	then.Fail = t
	return nil
}

// stringOrList reads a string or a list of strings. A missing value gives none.
func stringOrList(md toml.MetaData, prim toml.Primitive) ([]string, error) {
	var one string
	if err := md.PrimitiveDecode(prim, &one); err == nil {
		if one == "" {
			return nil, nil
		}
		return []string{one}, nil
	}
	var many []string
	if err := md.PrimitiveDecode(prim, &many); err != nil {
		return nil, err
	}
	return many, nil
}
