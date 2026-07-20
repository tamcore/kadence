package skill_test

import (
	"testing"

	"github.com/tamcore/kadence/internal/chat/skill"
)

func TestLoadEmbeddedSkills(t *testing.T) {
	reg, err := skill.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(reg.List()) < 2 {
		t.Fatalf("expected >=2 embedded skills, got %d", len(reg.List()))
	}
	if _, ok := reg.Get("workout-programming"); !ok {
		t.Fatal("workout-programming skill not found")
	}
	if _, ok := reg.Get("memory"); !ok {
		t.Fatal("memory skill not found")
	}
}

func TestGetReturnsBody(t *testing.T) {
	reg, _ := skill.Load()
	s, ok := reg.Get("workout-programming")
	if !ok || s.Body == "" {
		t.Fatalf("workout-programming missing body: ok=%v", ok)
	}
	if s.Description == "" {
		t.Fatal("workout-programming missing description")
	}
}

func TestForToolGlobMatch(t *testing.T) {
	reg, _ := skill.Load()
	// workout-programming triggers include "*_workout" and "*create*workout*".
	if _, ok := reg.ForTool("garmin__create_strength_workout"); !ok {
		t.Fatal("expected workout skill to match garmin__create_strength_workout")
	}
	if _, ok := reg.ForTool("garmin__get_activities"); ok {
		t.Fatal("did not expect a workout skill match for get_activities")
	}
}

func TestForHistory(t *testing.T) {
	reg, _ := skill.Load()
	hist := reg.ForHistory()
	if len(hist) != 1 || hist[0].Name != "memory" {
		t.Fatalf("expected [memory] for history, got %+v", hist)
	}
	// history-triggered skills must NOT also match tool globs.
	if _, ok := reg.ForTool("history"); ok {
		t.Fatal("history token must not be treated as a tool glob")
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := skill.ParseForTest([]byte("no frontmatter here")); err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}
