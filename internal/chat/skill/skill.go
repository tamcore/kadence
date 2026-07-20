// Package skill provides embedded, on-demand "skills": focused domain guidance
// the chat service can load on demand or inject before specific tool calls,
// keeping the base system prompt small.
package skill

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed skills/*.md
var skillFS embed.FS

// historyTrigger is the special (non-glob) trigger token marking a skill for
// injection when RAG history notes are present.
const historyTrigger = "history"

// Skill is one unit of domain guidance.
type Skill struct {
	Name        string
	Description string
	Triggers    []string
	Body        string
}

// Registry holds the loaded skills.
type Registry struct {
	skills []Skill
	byName map[string]Skill
}

type frontmatter struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Triggers    []string `yaml:"triggers"`
}

// Load parses every embedded skill file into a Registry, failing fast on a
// malformed file, a missing name/body, or a duplicate name.
func Load() (*Registry, error) {
	entries, err := fs.Glob(skillFS, "skills/*.md")
	if err != nil {
		return nil, fmt.Errorf("skill: glob embedded skills: %w", err)
	}
	reg := &Registry{byName: make(map[string]Skill, len(entries))}
	for _, e := range entries {
		data, err := skillFS.ReadFile(e)
		if err != nil {
			return nil, fmt.Errorf("skill: read %s: %w", e, err)
		}
		sk, err := parse(data)
		if err != nil {
			return nil, fmt.Errorf("skill: parse %s: %w", e, err)
		}
		if sk.Name == "" {
			return nil, fmt.Errorf("skill: %s: missing name", e)
		}
		if sk.Body == "" {
			return nil, fmt.Errorf("skill: %s: empty body", e)
		}
		if _, dup := reg.byName[sk.Name]; dup {
			return nil, fmt.Errorf("skill: duplicate name %q", sk.Name)
		}
		reg.byName[sk.Name] = sk
		reg.skills = append(reg.skills, sk)
	}
	sort.Slice(reg.skills, func(i, j int) bool { return reg.skills[i].Name < reg.skills[j].Name })
	return reg, nil
}

// ParseForTest exposes parse for package tests.
func ParseForTest(data []byte) (Skill, error) { return parse(data) }

func parse(data []byte) (Skill, error) {
	s := string(data)
	const open = "---\n"
	const closeMark = "\n---\n"
	if !strings.HasPrefix(s, open) {
		return Skill{}, fmt.Errorf("missing frontmatter")
	}
	rest := s[len(open):]
	before, after, ok := strings.Cut(rest, closeMark)
	if !ok {
		return Skill{}, fmt.Errorf("unterminated frontmatter")
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(before), &fm); err != nil {
		return Skill{}, fmt.Errorf("yaml: %w", err)
	}
	body := strings.TrimSpace(after)
	return Skill{Name: fm.Name, Description: fm.Description, Triggers: fm.Triggers, Body: body}, nil
}

// List returns a copy of all skills, sorted by name.
func (r *Registry) List() []Skill { return append([]Skill(nil), r.skills...) }

// Get returns the named skill.
func (r *Registry) Get(name string) (Skill, bool) {
	s, ok := r.byName[name]
	return s, ok
}

// ForTool returns the first skill whose glob trigger matches toolName. The
// special historyTrigger token is never treated as a tool glob.
func (r *Registry) ForTool(toolName string) (Skill, bool) {
	for _, s := range r.skills {
		for _, t := range s.Triggers {
			if t == historyTrigger {
				continue
			}
			ok, err := path.Match(t, toolName)
			if err != nil {
				// Malformed pattern: skip, never fail a request.
				continue
			}
			if ok {
				return s, true
			}
		}
	}
	return Skill{}, false
}

// ForHistory returns the skills marked with the historyTrigger token.
func (r *Registry) ForHistory() []Skill {
	var out []Skill
	for _, s := range r.skills {
		if slices.Contains(s.Triggers, historyTrigger) {
			out = append(out, s)
		}
	}
	return out
}
