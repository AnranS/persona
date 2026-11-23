package prompts

import (
	"strings"
	"testing"
)

func TestSuperviseInterruptedOrphanedAndReviewCarryNextAction(t *testing.T) {
	for kind, want := range map[string]string{
		"interrupted": "retry",
		"orphaned":    "resume",
		"review":      "verify",
	} {
		text := RenderSupervise(Supervise{Entries: []SuperviseEntry{{ID: "todo_example", Title: "Example task", Kind: kind}}})
		if !strings.Contains(text, "；next="+want) {
			t.Errorf("%s has no event-local next action %q:\n%s", kind, want, text)
		}
		for _, manual := range []string{"## Task supervision", "### Review and communication", "Own supervised tasks' completion"} {
			if strings.Contains(text, manual) {
				t.Errorf("%s event contains the general manual %q", kind, manual)
			}
		}
	}
}
