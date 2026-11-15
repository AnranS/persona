package prompts

import (
	"strings"
	"testing"
)

// The tool description is part of the main conversation's routing policy: it must not send newly adopted work to the
// legacy background conversation (#5732).
func TestAgentSuperviseNamesMainForNewWork(t *testing.T) {
	description := Tool("agent_supervise").Description
	for _, want := range []string{
		"The main conversation gets those messages and reviews every newly adopted task",
		"the legacy switch never routes new work to the background supervisor",
	} {
		if !strings.Contains(description, want) {
			t.Errorf("agent_supervise description lacks %q:\n%s", want, description)
		}
	}
	if strings.Contains(description, "By default the background supervisor") {
		t.Fatalf("agent_supervise still names the legacy background as the default:\n%s", description)
	}
}
