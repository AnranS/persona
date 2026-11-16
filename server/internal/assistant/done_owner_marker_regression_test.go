package assistant

import (
	"context"
	"testing"

	"github.com/nwverse/persona/server/internal/prompts"
)

// Conditional guidance and quoted history are not new owner demands; independent direct demands still get through.
func TestOwnerMarkerConditionalAndQuotedOccurrences(t *testing.T) {
	for _, tc := range []struct {
		name, reply, verdict string
	}{
		{"only if is conditional", "The work is complete. Only if the release fails, you need to roll it back.", verdictDone},
		{"trailing if is conditional", "The work is complete. You need to roll it back if the release fails.", verdictDone},
		{"trailing only if is conditional", "The work is complete. You need to roll it back only if the release fails.", verdictDone},
		{"trailing even if is still direct", "The work is complete. You need to notify the owner even if the release fails.", verdictNeedsUser},
		{"trailing if does not hide an independent requirement", "You need to roll it back if the release fails. Otherwise, you need to approve the release.", verdictNeedsUser},
		{"condition on a contrasted clause does not hide the first requirement", "The work is complete. You need to approve the release, but roll it back if it fails.", verdictNeedsUser},
		{"but only if keeps the requirement conditional", "The work is complete. You need to roll it back, but only if the release fails.", verdictDone},
		{"contrast inside the condition stays conditional", "The work is complete. You need to roll it back if the release fails but the data is intact.", verdictDone},
		{"trailing if-question is a direct requirement", "The work is complete. You need to check if the release failed.", verdictNeedsUser},
		{"compound check if-question stays direct", "The work is complete. You need to check if the release failed and if the owner approved.", verdictNeedsUser},
		{"compound verify if-question stays direct", "The work is complete. You need to verify if the release failed or if the owner cancelled.", verdictNeedsUser},
		{"test object noun does not make an if-question", "The work is complete. You need to add a test if the bug reproduces.", verdictDone},
		{"check object noun does not make an if-question", "The work is complete. You need to add a check if the build fails.", verdictDone},
		{"governing check with an object keeps the trailing condition", "The work is complete. You need to check the test if the build fails.", verdictDone},
		{"adverb before check keeps the if-question direct", "The work is complete. You need to carefully check if the release failed.", verdictNeedsUser},
		{"coordinated governing verbs keep the if-question direct", "The work is complete. You need to check and verify if the release failed.", verdictNeedsUser},
		{"quoted trailing condition does not hide a direct requirement", `The work is complete. You need to approve the release, not follow the old runbook's "roll back if it fails" instruction.`, verdictNeedsUser},
		{"compound even if stays direct", "The work is complete. You need to notify the owner even if the release fails and if the owner is away.", verdictNeedsUser},
		{"trailing conditional notification is not immediate", "The work is complete. You need to let me know if the release fails.", verdictDone},
		{"otherwise after only if is direct", "Only if the release fails, you need to roll it back. Otherwise, you need to approve the release.", verdictNeedsUser},
		{"nested only-if question does not hide a direct requirement", "The report checks only if the deploy succeeded: you need to approve the release.", verdictNeedsUser},
		{"quoted direct requirement is historical", `The old runbook said, "Stop. You need to approve the release." That approval is already done.`, verdictDone},
		{"quoted direct requirement does not hide a later one", `The old runbook said, "Stop. You need to roll back." You need to approve the release.`, verdictNeedsUser},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &cannedJSON{answer: `{"verdict":"done","reason":"finished"}`}
			c, err := judgeTaskDoneWith(context.Background(), m, taskDoneInput{Title: "Write the spec", Replies: []prompts.TaskDoneReply{doneReply(tc.reply)}})
			if err != nil {
				t.Fatal(err)
			}
			if c.Verdict != tc.verdict || c.WouldClose {
				t.Fatalf("reply %q: verdict %q close %v, want %q false", tc.reply, c.Verdict, c.WouldClose, tc.verdict)
			}
		})
	}
}
