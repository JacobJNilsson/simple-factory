package task

import (
	"regexp"
	"slices"
	"strings"
)

// Types lists the Conventional Commits types that a task type and a branch prefix may take.
var Types = []string{"feat", "fix", "docs", "chore", "refactor", "test", "perf", "ci", "build"}

// maxSlug is the longest slug that BranchBase produces.
const maxSlug = 50

// titleType matches a leading Conventional Commits prefix such as "fix:" or "feat(api)!:".
var titleType = regexp.MustCompile(`^([a-z]+)(\([^)]*\))?!?:\s*`)

// titleVerbs maps the first word of a title to a type. A title with another first word has no keyword type.
var titleVerbs = map[string]string{
	"fix": "fix", "fixes": "fix", "repair": "fix", "resolve": "fix",
	"document": "docs", "docs": "docs",
	"refactor": "refactor", "simplify": "refactor", "extract": "refactor", "rename": "refactor",
	"test": "test", "tests": "test",
	"optimize": "perf", "speed": "perf",
	"bump": "chore", "clean": "chore",
}

// foldLetters turns common Latin letters with marks into ASCII, so a title such as "Läs fältet" keeps its words.
var foldLetters = strings.NewReplacer(
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "ā", "a", "æ", "ae",
	"ç", "c", "è", "e", "é", "e", "ê", "e", "ë", "e", "ì", "i", "í", "i", "î", "i", "ï", "i",
	"ñ", "n", "ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ø", "o",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ý", "y", "ÿ", "y", "ß", "ss",
)

// BranchType returns the Conventional Commits type for the task's branch.
// The Type section wins. Otherwise a leading type prefix in the title wins, then the first word of the title.
// A task with a repo falls back to feat, and a task with no repo to chore.
func (t Task) BranchType() string {
	if t.Type != "" {
		return t.Type
	}
	title := strings.ToLower(strings.TrimSpace(t.Title))
	if m := titleType.FindStringSubmatch(title); m != nil && slices.Contains(Types, m[1]) {
		return m[1]
	}
	first, _, _ := strings.Cut(title, " ")
	if typ, ok := titleVerbs[first]; ok {
		return typ
	}
	if t.Repo == "" {
		return "chore"
	}
	return "feat"
}

// BranchBase returns the branch name of the task before any collision suffix, as <type>/<slug>.
func (t Task) BranchBase() string {
	return t.BranchType() + "/" + slug(t.Title)
}

// slug turns a title into lower case ASCII words joined by "-". It cuts at a word boundary within maxSlug characters.
func slug(title string) string {
	title = strings.ToLower(strings.TrimSpace(title))
	if loc := titleType.FindStringSubmatchIndex(title); loc != nil && slices.Contains(Types, title[loc[2]:loc[3]]) {
		title = title[loc[1]:]
	}
	title = foldLetters.Replace(title)
	words := strings.FieldsFunc(title, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := ""
	for _, w := range words {
		next := w
		if out != "" {
			next = out + "-" + w
		}
		if len(next) > maxSlug {
			if out == "" {
				out = w[:maxSlug]
			}
			break
		}
		out = next
	}
	if out == "" {
		return "task"
	}
	return out
}
