package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/orcart/orcarttest"
)

func TestReproStockOrcaCodexFollowsLaunchDefaults(t *testing.T) {
	n, _, o, _, home := orcaNode(t)
	n.shellSettled = true
	cwd := mkdir(t, filepath.Join(home, "proj"))
	o.SetWorktrees(orcarttest.Worktree("wt-proj", cwd, "refs/heads/main"))
	o.Handle("settings.getAgentLaunch", func(map[string]any) any {
		return map[string]any{"settings": map[string]any{
			"agentDefaultArgs": map[string]string{"codex": "--sandbox read-only -m orca-model"},
		}}
	})
	var got []string
	o.Handle("terminal.send", func(p map[string]any) any {
		text, _ := p["text"].(string)
		_, line, _ := strings.Cut(strings.TrimSuffix(text, "\r"), " && ")
		cmd, ok := startFileCommand(line)
		if !ok {
			t.Fatalf("typed command %q has no start file", text)
		}
		match := startFileRE.FindStringSubmatch(cmd)
		if match == nil {
			t.Fatalf("start command %q is not codex-start", cmd)
		}
		var file codexStartFile
		b, err := os.ReadFile(match[1])
		if err != nil || json.Unmarshal(b, &file) != nil {
			t.Fatalf("read Codex start file: %v", err)
		}
		got = slices.Clone(file.Argv)
		now := time.Now()
		dir := mkdir(t, filepath.Join(home, ".codex", "sessions", now.Format("2006"), now.Format("01"), now.Format("02")))
		stamp := now.UTC().Format(time.RFC3339Nano)
		resolved, _ := filepath.EvalSymlinks(cwd)
		meta, _ := json.Marshal(map[string]any{"timestamp": stamp, "type": "session_meta", "payload": map[string]any{"id": startThread, "cwd": resolved, "cli_version": startRolloutVersion}})
		user, _ := json.Marshal(map[string]any{"timestamp": stamp, "type": "event_msg", "payload": map[string]any{"type": "item_completed", "item": map[string]any{"type": "UserMessage", "id": "u1", "content": []any{map[string]any{"type": "text", "text": file.Prompt}}}}})
		if err := os.WriteFile(filepath.Join(dir, "rollout-"+startThread+".jsonl"), []byte(string(meta)+"\n"+string(user)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return map[string]any{"send": map[string]any{"handle": p["terminal"], "accepted": true, "bytesWritten": len(text)}}
	})
	if _, err := request(t, n, "session.start", map[string]any{"cwd": cwd, "launcher": "codex", "prompt": "hello"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"codex", "--sandbox", "read-only", "-m", "orca-model"}
	if !slices.Equal(got, want) {
		t.Fatalf("Codex argv = %q, want Orca defaults %q", got, want)
	}
}
