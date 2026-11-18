package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/tmux"
)

const (
	livePeerBypassHead = `<cross-session-message from-name="Persona" from-mode="bypass">`
	livePeerBareHead   = `<cross-session-message from-name="Persona">`
)

// livePeerCase describes the receiver-side evidence available when one message is sent. An empty transcriptMode
// writes a transcript with no recorded permission mode; nil argv makes the process arguments unreadable.
type livePeerCase struct {
	transcriptMode string
	argv           []string
	footerMode     string
	receiverClass  string
	owner          bool
}

// sendLivePeer sends one message through the existing outside-Claude path and returns its envelope's opening tag.
// A nonempty footerMode is served by a fake tmux capture for the external terminal named in Claude's session file.
func sendLivePeer(t *testing.T, c livePeerCase) (string, error) {
	t.Helper()
	n, f, home := herdrNode(t)
	noPaneEnv(t)
	shortPeerWait(t, 500*time.Millisecond)
	oldArgv := processArgv
	processArgv = func(pid int) ([]string, error) {
		if pid == os.Getpid() && c.argv != nil {
			return c.argv, nil
		}
		return nil, errors.New("process arguments unavailable")
	}
	t.Cleanup(func() { processArgv = oldArgv })

	var transcript string
	if c.transcriptMode == "" {
		transcript = bgTranscript(t, home, home, sidA, "deploy")
	} else {
		transcript = modeTranscript(t, home, home, c.transcriptMode)
	}
	inbox := newFakeInbox(t, transcript)
	inbox.setMode(c.receiverClass)
	entrypoint := "claude-desktop"
	var extra map[string]any
	if c.footerMode != "" && runtime.GOOS == "windows" {
		t.Skip("the external tmux capture fixture is a POSIX shell script")
	}
	if c.footerMode != "" {
		footer := map[string]string{
			"default":           "────\n❯ \n────\n  ? for shortcuts\n",
			"bypassPermissions": "────\n❯ \n────\n  ⏵⏵ bypass permissions on (shift+tab to cycle)\n",
		}[c.footerMode]
		if footer == "" {
			t.Fatalf("no footer fixture for mode %q", c.footerMode)
		}
		dir := t.TempDir()
		screenPath := filepath.Join(dir, "screen")
		if err := os.WriteFile(screenPath, []byte(footer), 0o600); err != nil {
			t.Fatal(err)
		}
		bin := filepath.Join(dir, "tmux")
		script := "#!/bin/sh\n[ \"$1\" = capture-pane ] && /bin/cat " + shellQuote(screenPath) + "\nexit 0\n"
		if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		n.hosts.Set(append(n.hosts.All(), &tmuxHost{c: tmux.Client{Bin: bin}})...)
		entrypoint = "cli"
		extra = map[string]any{"tmux": "orc:@11.%11"}
	}
	writeOutsideClaude(t, home, sidA, entrypoint, "idle", inbox, extra)
	f.reply("session.snapshot", snapshot(nil, nil, nil, nil))
	if c.footerMode != "" {
		proc, ok := n.outsideClaude(sidA)
		if !ok {
			t.Fatal("the external-terminal Claude is not live")
		}
		pane := outsidePane(proc)
		captured, err := n.readPane(context.Background(), pane, "visible", 60)
		if pane == "" || err != nil || tuiMode(captured) != c.footerMode {
			t.Fatalf("external footer %q: pane %q, capture %q, %v", c.footerMode, pane, captured, err)
		}
	}

	params := map[string]any{"session_id": sidA, "text": "ship it", "client_msg_id": "live-peer-1"}
	if c.owner {
		params["owner_input"] = true
	}
	_, err := request(t, n, "session.send", params)
	return envelopeHead(t, inbox, 0), err
}

func checkLivePeer(t *testing.T, head string, err error, wantHead, wantCode string) {
	t.Helper()
	if head != wantHead {
		t.Errorf("envelope = %q, want %q", head, wantHead)
	}
	if wantCode == "" {
		if err != nil {
			t.Errorf("receiver did not take the message: %v (code %q)", err, refusalCode(err))
		}
	} else if err == nil || refusalCode(err) != wantCode || !maybeDelivered(err) {
		t.Errorf("send outcome = %v (code %q), want delivered-unknown %q", err, refusalCode(err), wantCode)
	}
}

// AC1: plan is a bypass-class session when its live process was started with bypass available. Without that process
// evidence, plan keeps its prior unattested behavior rather than guessing bypass.
func TestOutsideClaudeOwnerPlanWithBypassArgv(t *testing.T) {
	t.Run("bypass available", func(t *testing.T) {
		head, err := sendLivePeer(t, livePeerCase{
			transcriptMode: "plan",
			argv:           []string{"claude", "--dangerously-skip-permissions"},
			receiverClass:  peerModeBypass,
			owner:          true,
		})
		checkLivePeer(t, head, err, livePeerBypassHead, "")
	})
	t.Run("bypass unavailable", func(t *testing.T) {
		head, err := sendLivePeer(t, livePeerCase{
			transcriptMode: "plan",
			argv:           []string{"claude", "--resume", sidA},
			receiverClass:  peerModeBypass,
			owner:          true,
		})
		checkLivePeer(t, head, err, livePeerBareHead, peerHeld)
	})
}

// AC2: immediately after default becomes bypass, the live footer is newer evidence than the transcript. Cover both a
// stale default record and a transcript that has not recorded a mode yet.
func TestOutsideClaudeOwnerLiveFooterDefaultToBypass(t *testing.T) {
	for _, mode := range []string{"default", ""} {
		name := mode
		if name == "" {
			name = "no recorded mode"
		}
		t.Run(name, func(t *testing.T) {
			head, err := sendLivePeer(t, livePeerCase{
				transcriptMode: mode,
				footerMode:     "bypassPermissions",
				receiverClass:  peerModeBypass,
				owner:          true,
			})
			checkLivePeer(t, head, err, livePeerBypassHead, "")
		})
	}
}

// AC3: immediately after bypass becomes default, the live footer must replace the stale bypass attestation. The
// process's bypass-enabling flag says only that bypass is available, not that it is still the current mode.
func TestOutsideClaudeOwnerLiveFooterBypassToDefault(t *testing.T) {
	head, err := sendLivePeer(t, livePeerCase{
		transcriptMode: "bypassPermissions",
		argv:           []string{"claude", "--allow-dangerously-skip-permissions"},
		footerMode:     "default",
		receiverClass:  peerModePrompting,
		owner:          true,
	})
	checkLivePeer(t, head, err, livePeerBareHead, "")
}

// AC4: receiver-side evidence never authorizes somebody else's words. With no readable footer or bypass-enabling
// argv, the existing fresh-transcript classification remains, including no guess when the transcript states no mode.
func TestOutsideClaudePeerEvidenceOwnerOnlyAndFallback(t *testing.T) {
	t.Run("non-owner with live bypass evidence", func(t *testing.T) {
		head, err := sendLivePeer(t, livePeerCase{
			transcriptMode: "default",
			argv:           []string{"claude", "--dangerously-skip-permissions"},
			footerMode:     "bypassPermissions",
			receiverClass:  peerModeBypass,
		})
		checkLivePeer(t, head, err, livePeerBareHead, peerHeld)
	})
	t.Run("fresh bypass transcript", func(t *testing.T) {
		head, err := sendLivePeer(t, livePeerCase{
			transcriptMode: "bypassPermissions",
			receiverClass:  peerModeBypass,
			owner:          true,
		})
		checkLivePeer(t, head, err, livePeerBypassHead, "")
	})
	t.Run("fresh default transcript", func(t *testing.T) {
		head, err := sendLivePeer(t, livePeerCase{
			transcriptMode: "default",
			receiverClass:  peerModePrompting,
			owner:          true,
		})
		checkLivePeer(t, head, err, livePeerBareHead, "")
	})
	t.Run("no recorded mode never guesses bypass", func(t *testing.T) {
		head, err := sendLivePeer(t, livePeerCase{
			receiverClass: peerModeBypass,
			owner:         true,
		})
		checkLivePeer(t, head, err, livePeerBareHead, peerHeld)
	})
}
