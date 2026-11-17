package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// An older hub still names a runtime (assistant.warm, assistant.send, session.send, a guest's start and a meeting's
// room): the node ignores it. Every conversation starts on the CLI command (claude -p through the launcher), a message
// on one already running goes to the same process, and nothing of another runtime is unpacked.
func TestAssistantIgnoresARuntimeFromAnOlderHub(t *testing.T) {
	for _, runtime := range []any{"agentsdk", "cli", "another", nil} {
		name, _ := json.Marshal(runtime)
		t.Run("runtime="+string(name), func(t *testing.T) {
			f := cliTest(t)
			cwd := t.TempDir()
			call := func(method string, params map[string]any) string {
				t.Helper()
				params["runtime"] = runtime
				raw, err := nodeCall(t, f.n, method, params)
				if err != nil {
					t.Fatalf("%s: %v", method, err)
				}
				var out struct {
					SessionID string `json:"session_id"`
				}
				_ = json.Unmarshal(raw, &out)
				return out.SessionID
			}
			onCLI := func(what string, p *assistantProc) {
				t.Helper()
				if p == nil || !slices.Contains(p.cmd.Args, "-ic") || !slices.Contains(p.cmd.Args, "--input-format") {
					t.Fatalf("%s does not run claude -p through the launcher: %+v", what, p)
				}
			}

			call("assistant.warm", map[string]any{"launcher": "claude", "cwd": cwd, "model_fingerprint": "fp"})
			f.n.mu.Lock()
			spare := f.n.spare
			f.n.mu.Unlock()
			onCLI("the warm spare", spare)

			id := call("assistant.send", map[string]any{"launcher": "claude", "cwd": cwd, "text": "hello", "model_fingerprint": "fp"})
			f.idle(t, id)
			first := f.proc(id)
			onCLI("a new conversation", first)
			if !strings.Contains(f.file("cli-argv"), "--session-id "+id) {
				t.Fatalf("claude args: %s", f.file("cli-argv"))
			}

			call("session.send", map[string]any{"session_id": id, "text": "again", "model_fingerprint": "fp"})
			f.idle(t, id)
			if f.proc(id) != first {
				t.Fatal("a runtime on session.send restarted the conversation")
			}
			if strings.Count(f.file("stdin.jsonl"), `"type":"user"`) != 2 {
				t.Fatalf("messages written: %s", f.file("stdin.jsonl"))
			}

			guest := call("assistant.send", map[string]any{"launcher": "claude", "cwd": t.TempDir(), "text": "hi",
				"guest": map[string]any{"tools": []string{"lark_agent_reply"}}})
			onCLI("a guest", f.proc(guest))
			if args := f.proc(guest).cmd.Args; !slices.Contains(args, "--strict-mcp-config") {
				t.Fatalf("the guest's args: %q", args)
			}

			rec := "0f8fad5b-d9cb-469f-a165-70867728950e"
			out, err := f.n.assistantGuestPrepare(json.RawMessage(`{"dir":".persona/guest-meeting/` + rec + `","claude_md":"# room","kind":"meeting"}`))
			if err != nil {
				t.Fatal(err)
			}
			roomDir, _ := out.(map[string]any)["cwd"].(string)
			room := "7d4a3c1e-1111-4222-8333-444455556666"
			if got := call("assistant.send", map[string]any{"launcher": "claude", "cwd": roomDir, "text": "the header",
				"new_session_id": room, "guest": map[string]any{"tools": []string{"meeting_lines"}, "kind": guestMeeting}}); got != room {
				t.Fatalf("the room started as %q, want %q", got, room)
			}
			onCLI("a meeting's room", f.proc(room))

			if _, err := os.Stat(filepath.Join(f.n.dir, "runtime-packages")); !os.IsNotExist(err) {
				t.Fatalf("a runtime package was unpacked: %v", err)
			}
			for _, sid := range []string{id, guest, room} {
				st, _ := f.n.state.get(sid)
				if raw, _ := json.Marshal(st); strings.Contains(string(raw), `"runtime"`) {
					t.Fatalf("the state records a runtime: %s", raw)
				}
			}
		})
	}
}

// A conversation an older node ran on another runtime has "runtime" in its state as the hub keeps it. It continues on
// the CLI: claude resumes the same conversation id in the profile its state recorded, whether the hub still names
// that runtime or names none; the state the node writes back has no runtime.
func TestAssistantResumesAConversationAnOlderNodeRanOnAnotherRuntime(t *testing.T) {
	for _, hub := range []string{"older hub", "current hub"} {
		t.Run(hub, func(t *testing.T) {
			f, _ := resumeTest(t)
			n := f.n
			id := "a5500000-0000-4000-8000-000000000901"
			cwd := filepath.Join(n.dir, "assistant")
			profile := filepath.Join(n.dir, "profiles", "claude")
			writeProfileTranscript(t, profile, cwd, id)
			// state.load's answer as the hub gives it, with what an older node wrote for the conversation.
			load := []byte(`{"sessions":{"` + id + `":{"launcher":"claude","cwd":` + jsonString(t, cwd) + `,` +
				`"model_fingerprint":"fp-main","runtime":"agentsdk","config_dir":` + jsonString(t, profile) + `,` +
				`"is_main":true,"started_at":"2026-11-01T00:00:00Z"}}}`)
			var res struct {
				Sessions map[string]sessionState `json:"sessions"`
			}
			if err := json.Unmarshal(load, &res); err != nil {
				t.Fatal(err)
			}
			n.state.merge(res.Sessions)

			params := map[string]any{"session_id": id, "launcher": "claude", "cwd": cwd, "text": "hello", "model_fingerprint": "fp-main", "is_main": true}
			if hub == "older hub" {
				params["runtime"] = "agentsdk"
			}
			raw, err := nodeCall(t, n, "assistant.send", params)
			if err != nil {
				t.Fatalf("resume failed: %v (claude saw %q)", err, f.file("cli-profile"))
			}
			var out struct {
				SessionID string `json:"session_id"`
			}
			if json.Unmarshal(raw, &out) != nil || out.SessionID != id {
				t.Fatalf("answered %s, want the same conversation %s", raw, id)
			}
			p := f.proc(id)
			if p == nil {
				t.Fatal("no process")
			}
			if i := slices.Index(p.cmd.Args, "--resume"); i < 0 || i+1 >= len(p.cmd.Args) || p.cmd.Args[i+1] != id ||
				!slices.Contains(p.cmd.Args, "-ic") || slices.Contains(p.cmd.Args, "--session-id") {
				t.Fatalf("not claude -p resuming %s: %q", id, p.cmd.Args)
			}
			if got := envValue(p.cmd.Environ(), "CLAUDE_CONFIG_DIR"); !sameScopePath(got, profile) {
				t.Fatalf("process profile %q, want %q", got, profile)
			}
			deadline := time.Now().Add(5 * time.Second)
			for f.file("cli-profile") == "" && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			if got := strings.TrimSuffix(f.file("cli-profile"), "\n"); !sameScopePath(strings.TrimPrefix(got, "set:"), profile) {
				t.Fatalf("claude ran in profile %q, want %q", got, profile)
			}
			st, _ := n.state.get(id)
			if !sameScopePath(st.ConfigDir, profile) || st.Launcher != "claude" {
				t.Fatalf("state %+v", st)
			}
			if b, _ := json.Marshal(st); strings.Contains(string(b), `"runtime"`) {
				t.Fatalf("the state written back records a runtime: %s", b)
			}
		})
	}
}

// A CLI conversation's execution fingerprint is the one an older node computed for it (golden, from origin/main at
// 5fe749b57b), so a node update does not restart the conversations it runs; a runtime of "cli" counted as none there.
func TestCLIExecutionFingerprintIsUnchanged(t *testing.T) {
	n, _ := testNode(t)
	for _, c := range []struct {
		m    modelExecution
		want string
	}{
		{modelExecution{}, "5544069c813ca3c85cbaad795109f79164da3209f4af64a29c0def96499ce994"},
		{modelExecution{Model: "claude-opus-4-1", Effort: "high", UseEffort: true, Fingerprint: "binding-1", IsMain: true,
			FollowsSetting: true, MainCompactWindow: 400000, SupervisionInstructions: "role"},
			"8d6a7d05620513ee720dfb253343240b789761f6c142b6878c189b5f56d3b3fd"},
		// what an older node computed for {Model, Fingerprint, Runtime: "cli"}
		{modelExecution{Model: "claude-opus-4-1", Fingerprint: "binding-1"}, "5c300fcdd50cbc8adced417983961abdc858273c17bee69eb73debdff7fccc65"},
	} {
		if got := n.executionFingerprint("claude", c.m); got != c.want {
			raw, _ := json.Marshal(c.m)
			t.Errorf("%s: fingerprint %s, want %s", raw, got, c.want)
		}
	}
}
