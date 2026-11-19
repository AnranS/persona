package assistant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #10224 added inline observation without a prompt-size budget increase. Keep that total budget explicit while the
// per-piece baseline continues to be enforced and lowered through TestPromptSizeRatchet.
func TestAgentWaitPromptSizeBudget(t *testing.T) {
	const budget = 289184
	total := 0
	for _, size := range promptSizePieces(t) {
		total += size
	}
	if total > budget {
		t.Errorf("measured prompt size is %d bytes, over the %d-byte budget by %d; compact the wait_seconds guidance instead of raising the baseline", total, budget, total-budget)
	}
}

// TestAgentWaitPromptContract checks the meaning of the inline-observation guidance, not its current prose. The
// golden is the final tool list whose wire form TestGoldenMCPTools compares with the hub's actual tools.
func TestAgentWaitPromptContract(t *testing.T) {
	path := filepath.Join("..", "hub", "testdata", "prompts", "mcp_tools.assistant.golden")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var listed []struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		InputSchema struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"input_schema"`
	}
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	tools := make(map[string]int, len(listed))
	for i, tool := range listed {
		if _, duplicate := tools[tool.Name]; duplicate {
			t.Fatalf("%s: duplicate tool %q", path, tool.Name)
		}
		tools[tool.Name] = i
	}

	sharedInstructions := waitPromptSharedInstructions(t)
	for _, name := range []string{"agent_send", "agent_start", "task_plan"} {
		i, ok := tools[name]
		if !ok {
			t.Errorf("%s: tool %q is missing", path, name)
			continue
		}
		t.Run(name, func(t *testing.T) {
			tool := listed[i]
			rawWait, ok := tool.InputSchema.Properties["wait_seconds"]
			if !ok {
				t.Fatal("wait_seconds is missing from input_schema.properties")
			}
			var wait struct {
				Type        string `json:"type"`
				Minimum     *int   `json:"minimum"`
				Description string `json:"description"`
			}
			if err := json.Unmarshal(rawWait, &wait); err != nil {
				t.Fatalf("wait_seconds: %v", err)
			}
			if wait.Type != "integer" {
				t.Errorf("wait_seconds type = %q, want integer", wait.Type)
			}
			if wait.Minimum == nil {
				t.Error("wait_seconds minimum is missing, want 0")
			} else if *wait.Minimum != 0 {
				t.Errorf("wait_seconds minimum = %d, want 0", *wait.Minimum)
			}
			for _, required := range tool.InputSchema.Required {
				if required == "wait_seconds" {
					t.Error("wait_seconds must be optional, not required")
				}
			}

			// The contract may live in shared instructions, the tool description, the schema description, or a
			// combination of them. Checking their union allows shorter shared wording and moves between watched pieces.
			allGuidance := tool.Description + "\n" + wait.Description + "\n" + sharedInstructions

			requireWaitPromptClause(t, allGuidance, "omission and zero preserve the ordinary receipt",
				`omit(?:ted|ting)?|omission|absent`, `zero|\b0\b`, `receipt`, `ordinary|current|normal|usual|immediate|unchanged|only|keep|retain|preserve|return`)
			requireWaitPromptClause(t, allGuidance, "positive waits observe for at most 50 seconds after dispatch",
				`wait_seconds`, `observ\w*`, `at most|up to|capp?ed|max(?:imum)?\b`, `50\s*(?:seconds|s\b)`, `dispatch`)
			requireWaitPromptClause(t, allGuidance, "inline observation", `inline`, `observ\w*|result`)
			requireWaitPromptClause(t, allGuidance, "a completed reply", `observ\w*|inline`, `completed`, `reply`)
			requireWaitPromptClause(t, allGuidance, "an existing question reference", `observ\w*|inline`, `question`, `interaction_id|interaction(?: question)? reference`)
			for _, state := range []string{"timeout", "cancelled", "unavailable", "interrupted", "failed"} {
				requireWaitPromptClause(t, allGuidance, "the "+state+" result", `observ\w*|inline`, `\b`+state+`\b`)
			}
			requireWaitPromptClause(t, allGuidance, "known input and session references",
				`record|result|observ\w*`, `input`, `session`, `known|available`, `reference|correlat\w*|(?:^|\W)ids?(?:\W|$)`)

			for _, forbidden := range []struct {
				action   string
				patterns []string
			}{
				{"answering questions", []string{`answer\w*`, `question`}},
				{"accepting tasks", []string{`accept\w*`, `task`}},
				{"resending input", []string{`re-?send\w*|resend\w*`}},
				{"cancelling work", []string{`cancel\w*`}},
			} {
				requireWaitPromptClause(t, allGuidance, "observation never performs "+forbidden.action,
					append([]string{`never|\bno\b|\bnot\b|without|must\s+not`}, forbidden.patterns...)...)
			}
			requireWaitPromptClause(t, allGuidance, "normal supervision still applies",
				`supervis\w*`, `task_review`, `normal|usual|still|continu\w*|remain\w*|appl\w*`)
			requireWaitPromptClause(t, allGuidance, "older nodes time out rather than attribute an unrelated reply",
				`older|legacy`, `node`, `time\s*out|timeout`, `unrelated|another|wrong`, `reply`)
			if name == "task_plan" {
				requireWaitPromptClause(t, allGuidance, "only an immediately dispatchable single-task plan waits",
					`only`, `immediate`, `dispatch`, `single|one`, `task`, `wait`)
			}
		})
	}
}

// waitPromptSharedInstructions includes every generated shared-instructions variant, so a rule can move to the
// common guidance without the contract test depending on which assistant prompt carries it.
func waitPromptSharedInstructions(t *testing.T) string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "prompts", "assistant_claude_md.*.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no assistant shared instruction goldens found")
	}
	var instructions strings.Builder
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		instructions.Write(raw)
		instructions.WriteByte('\n')
	}
	return instructions.String()
}

var waitPromptClauseBoundary = regexp.MustCompile(`[.!?](?:\s|$)|\n+`)

// requireWaitPromptClause permits compaction and reordering while keeping related ideas in the same sentence or line.
func requireWaitPromptClause(t *testing.T, text, contract string, patterns ...string) {
	t.Helper()
	for _, clause := range waitPromptClauseBoundary.Split(text, -1) {
		matches := true
		for _, pattern := range patterns {
			if !regexp.MustCompile(`(?i)` + pattern).MatchString(clause) {
				matches = false
				break
			}
		}
		if matches {
			return
		}
	}
	t.Errorf("guidance does not express %s: want one clause matching %v", contract, patterns)
}
