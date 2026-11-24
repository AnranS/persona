package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/db"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/rpc"
)

type blockingStartHub struct {
	Hub
	entered chan struct{}
	release chan struct{}
}

func (b *blockingStartHub) DeviceCall(ctx context.Context, p DeviceCall) (json.RawMessage, error) {
	if p.Method == DeviceSessionStart {
		select {
		case b.entered <- struct{}{}:
		default:
		}
		<-b.release
	}
	return b.Hub.DeviceCall(ctx, p)
}

func todoHandoffHTTPReq(t *testing.T, s *Service, method, path, body, role string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if role != "" {
		req.Header.Set(RoleHeader, role)
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, req)
	return w
}

func setupHandoffFixture(t *testing.T, store Store, srcLauncher, srcAgent, srcPerm string, srcSess proto.Session) (*threadEnv, Todo, TaskBrief) {
	t.Helper()
	e := newThreadEnv(t, store)
	ctx := context.Background()
	e.f.launcherList = []proto.Launcher{
		{Name: "claude", Agent: "claude"},
		{Name: "profiles-ag", Agent: "claude"},
		{Name: "codex", Agent: "codex"},
	}
	e.f.ownModels = true

	todo, err := e.s.createTaskTodo(ctx, "Fix auth token refresh race", "Investigate and fix the refresh race.", "m1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	brief, err := e.s.putTaskBrief(ctx, briefRecord{
		StartedBy:    briefByOwner,
		Conversation: "m1",
		Goal:         "Eliminate duplicate refresh calls under concurrent 401s",
		Request:      &BriefRequest{Text: "Fix the refresh race and add regression tests", At: time.Now().UTC()},
		DoneWhen:     []string{"go test -race passes", "no duplicate refresh requests"},
		OutOfBounds:  []string{"Do not change wire schema"},
	}, todo.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.s.linkTodoAgent(ctx, todo.ID, "box", srcSess.SessionID, todo.Title, "/code/app", srcAgent); err != nil {
		t.Fatal(err)
	}
	if st := e.s.todoDB(); st != nil {
		_, err := st.InsertTodoDecision(ctx, db.TodoDecisionRow{
			ID:        "dec_1",
			TodoID:    todo.ID,
			Question:  "How should refresh be serialized?",
			Choice:    "Single-flight per token key",
			Reason:    "Avoids lock contention across unrelated tokens",
			Source:    db.DecisionCard,
			CreatedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	srcSess.MachineID = "box"
	srcSess.Cwd = "/code/app"
	srcSess.Agent = srcAgent
	srcSess.Launcher = srcLauncher
	srcSess.PermissionMode = srcPerm
	if srcSess.UpdatedAt.IsZero() {
		srcSess.UpdatedAt = time.Now().UTC()
	}
	e.sessions("box", srcSess)

	e.f.nodes = map[string]nodeAnswer{
		DeviceSessionItems: func(_ string, _ json.RawMessage) (any, error) {
			return map[string]any{
				"items": []proto.Item{
					{Seq: 1, Role: "user", Kind: proto.KindText, Text: "Fix the refresh race in server/auth.go"},
					{Seq: 2, Role: "assistant", Kind: proto.KindToolUse, ToolName: "Edit", ToolUseID: "t1",
						ToolInput: json.RawMessage(`{"file_path":"server/auth.go"}`)},
					{Seq: 3, Role: "assistant", Kind: proto.KindToolUse, ToolName: "Bash", ToolUseID: "t2",
						ToolInput: json.RawMessage(`{"command":"go test ./internal/auth"}`)},
					{Seq: 4, Role: "user", Kind: proto.KindToolResult, ToolUseID: "t2", IsError: true,
						Text: "FAIL github.com/nwverse/persona/server/internal/auth\nexit code: 1"},
					{Seq: 5, Role: "assistant", Kind: proto.KindText, Text: "Identified the race in refreshLocked; updating tests next."},
				},
				"has_more": false,
			}, nil
		},
		"git.review": func(_ string, _ json.RawMessage) (any, error) {
			return proto.GitReview{
				Root:  "/code/app",
				Scope: proto.ReviewUncommitted,
				Base:  proto.GitReviewBase{Hash: "1111222233334444", Short: "1111222", Subject: "base commit"},
				Head:  &proto.GitReviewHead{Hash: "1111222233334444", Short: "1111222", Branch: "fix/auth-race"},
				Files: []proto.GitReviewFile{
					{Path: "server/auth.go", Status: "modified", Added: 18, Deleted: 4},
					{Path: ".env.local", Status: "modified", Refused: true},
				},
			}, nil
		},
	}
	return e, todoNow(e.s, todo.ID), brief
}

// Acceptance Criterion A1:
// On authorized Claude Code -> Codex and Claude profile -> different profile continuations,
// the same to-do retains its brief, decisions, and workspace; the new Agent receives the saved
// handoff before the task prompt; the owner can inspect exact text, generator, source ranges,
// diff base, gaps, and delivery status.
func TestTodoContextHandoffClaudeToCodexAndProfileSwitch(t *testing.T) {
	cases := []struct {
		name           string
		targetLauncher string
		targetAgent    string
	}{
		{name: "claude_to_codex", targetLauncher: "codex", targetAgent: "codex"},
		{name: "claude_profile_to_different_profile", targetLauncher: "profiles-ag", targetAgent: "claude"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, todo, brief := setupHandoffFixture(t, nil, "claude", "claude", "acceptEdits", proto.Session{
				SessionID: "agent01",
				Status:    proto.StatusIdle,
				State:     proto.StateIdle,
			})
			ctx := context.Background()

			// Even with user_requested=true and readonly=false, a cross-launcher continuation must wait
			// for explicit owner approval on the plan card rather than auto-starting.
			args, _ := json.Marshal(map[string]any{
				"tasks": []map[string]any{{
					"title":          "Continue auth fix on " + tc.targetLauncher,
					"prompt":         "Finish the remaining auth refresh test and run go test -race.",
					"machine":        "box",
					"cwd":            "/code/app",
					"launcher":       tc.targetLauncher,
					"permission":     "acceptEdits",
					"role":           "worker",
					"user_requested": true,
					"continues":      todo.ID,
					"continues_why":  "Switch launcher to continue the same to-do",
				}},
			})
			planRes, err := e.s.toolTaskPlan(ctx, "m1", args)
			if err != nil {
				t.Fatalf("toolTaskPlan: %v", err)
			}
			if !strings.Contains(planRes, "nothing has started") {
				t.Fatalf("cross-launcher continuation auto-started without owner approval: %s", planRes)
			}
			if len(e.f.starts) != 0 {
				t.Fatalf("started before owner approval: %d", len(e.f.starts))
			}

			planID := planIDRe.FindString(planRes)
			if planID == "" {
				t.Fatalf("no plan id in response: %s", planRes)
			}
			// Owner explicitly approves and starts the plan.
			if _, err := e.s.startPlan(ownerStart(ctx), planID, nil, false); err != nil {
				t.Fatalf("startPlan: %v", err)
			}
			waitFor(t, "replacement agent linked", func() bool {
				return len(todoNow(e.s, todo.ID).Agents) == 2 && e.s.taskStarting.Load() == 0
			})

			// Same to-do retains brief, workspace, and historical link.
			gotTodo := todoNow(e.s, todo.ID)
			if todoCount(e.s) != 1 {
				t.Fatalf("created duplicate to-do: count=%d", todoCount(e.s))
			}
			keptBrief, ok := e.s.briefOfTodo(todo.ID)
			if !ok || keptBrief.ID != brief.ID {
				t.Fatalf("brief not retained: %+v", keptBrief)
			}
			if gotTodo.Agents[0].ReleasedAt == nil {
				t.Fatalf("predecessor not released into history: %+v", gotTodo.Agents[0])
			}
			replacement := gotTodo.Agents[1]
			if replacement.Cwd != "/code/app" {
				t.Fatalf("workspace not retained: cwd=%q", replacement.Cwd)
			}

			// Inspect via GET /api/v1/todos/{id} and GET /api/v1/todos/{id}/handoffs.
			detailW := todoHandoffHTTPReq(t, e.s, "GET", "/api/v1/todos/"+todo.ID, "", "")
			if detailW.Code != http.StatusOK {
				t.Fatalf("GET /api/v1/todos/%s = %d %s", todo.ID, detailW.Code, detailW.Body.String())
			}
			var detail struct {
				Handoff *TodoContextHandoff `json:"handoff"`
			}
			if err := json.Unmarshal(detailW.Body.Bytes(), &detail); err != nil {
				t.Fatalf("decode detail: %v", err)
			}
			if detail.Handoff == nil {
				t.Fatalf("expected optional handoff on to-do detail: %s", detailW.Body.String())
			}
			h := detail.Handoff
			if h.Generator != handoffGeneratorDeterministicV1 || h.Strategy != handoffStrategyFreshFull {
				t.Fatalf("unexpected generator/strategy: %+v", h)
			}
			if h.DeliveryStatus != handoffDeliveryAccepted || h.TargetSession != replacement.SessionID || h.DeliveredAt == nil {
				t.Fatalf("delivery not recorded as accepted: %+v", h)
			}
			if h.SourceSession != "agent01" || h.SourceLauncher != "claude" || h.TargetLauncher != tc.targetLauncher {
				t.Fatalf("source/target metadata mismatch: %+v", h)
			}
			if !strings.HasPrefix(h.BriefRevision, brief.ID+"@") {
				t.Fatalf("brief_revision mismatch: %q", h.BriefRevision)
			}
			if len(h.SourceRanges) != 1 || h.SourceRanges[0].FromSeq != 1 || h.SourceRanges[0].ToSeq != 5 || h.SourceRanges[0].ItemCount != 5 {
				t.Fatalf("source_ranges mismatch: %+v", h.SourceRanges)
			}
			if h.DiffRef == nil || h.DiffRef.Scope != proto.ReviewUncommitted || h.DiffRef.BaseShort != "1111222" || h.DiffRef.Branch != "fix/auth-race" {
				t.Fatalf("diff_ref mismatch: %+v", h.DiffRef)
			}
			if !strings.Contains(h.Summary, "server/auth.go (+18/-4)") || !strings.Contains(h.Summary, ".env.local [refused]") {
				t.Fatalf("summary missing workspace snapshot or refused marker:\n%s", h.Summary)
			}
			if utf8.RuneCountInString(h.Summary) > todoHandoffMaxRunes {
				t.Fatalf("summary exceeded cap: %d runes", utf8.RuneCountInString(h.Summary))
			}

			// Verify the replacement Agent received the EXACT saved handoff BEFORE the task prompt!
			e.f.mu.Lock()
			starts := append([]map[string]any(nil), e.f.starts...)
			e.f.mu.Unlock()
			if len(starts) != 1 {
				t.Fatalf("expected 1 start, got %d", len(starts))
			}
			sentPrompt, _ := starts[0]["prompt"].(string)
			handoffIdx := strings.Index(sentPrompt, h.Summary)
			taskIdx := strings.Index(sentPrompt, "Finish the remaining auth refresh test")
			if handoffIdx != 0 || taskIdx <= handoffIdx {
				t.Fatalf("saved handoff was not injected before the task prompt: handoffIdx=%d taskIdx=%d", handoffIdx, taskIdx)
			}
			if starts[0]["permission_mode"] != "acceptEdits" {
				t.Fatalf("switch changed permission mode: got %v, want acceptEdits", starts[0]["permission_mode"])
			}

			// GET /api/v1/todos/{id}/handoffs lists the same artifact.
			listW := todoHandoffHTTPReq(t, e.s, "GET", "/api/v1/todos/"+todo.ID+"/handoffs", "", "")
			if listW.Code != http.StatusOK {
				t.Fatalf("GET handoffs = %d %s", listW.Code, listW.Body.String())
			}
			var listed struct {
				Handoffs []TodoContextHandoff `json:"handoffs"`
			}
			if err := json.Unmarshal(listW.Body.Bytes(), &listed); err != nil || len(listed.Handoffs) != 1 || listed.Handoffs[0].ID != h.ID {
				t.Fatalf("handoffs list mismatch: %+v err=%v", listed, err)
			}

			// Extensions cannot inspect handoff content or execute handoffs.
			extDetail := todoHandoffHTTPReq(t, e.s, "GET", "/api/v1/todos/"+todo.ID, "", roleExtension)
			if extDetail.Code == http.StatusOK {
				var extBody struct {
					Handoff *TodoContextHandoff `json:"handoff"`
				}
				_ = json.Unmarshal(extDetail.Body.Bytes(), &extBody)
				if extBody.Handoff != nil {
					t.Fatal("extension token received private handoff on to-do detail")
				}
			}
			if extList := todoHandoffHTTPReq(t, e.s, "GET", "/api/v1/todos/"+todo.ID+"/handoffs", "", roleExtension); extList.Code != http.StatusForbidden {
				t.Fatalf("extension GET handoffs = %d, want 403", extList.Code)
			}
			if extPost := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff", `{"launcher":"codex"}`, roleExtension); extPost.Code != http.StatusForbidden {
				t.Fatalf("extension POST handoff = %d, want 403", extPost.Code)
			}
		})
	}
}

// Acceptance Criterion A2 (part 1):
// With 503, expired login, or exhausted quota fixtures, the owner can select an eligible target
// without requiring a response from the old model. Also verifies preview and regeneration superseding.
func TestTodoContextHandoffInterruptionFixturesWithoutOldModelResponse(t *testing.T) {
	fixtures := []struct {
		name string
		sess proto.Session
	}{
		{
			name: "gateway_503",
			sess: proto.Session{
				SessionID:     "agent01",
				Status:        proto.StatusDone,
				State:         proto.StateIdle,
				TurnError:     "API Error: 503 Service Unavailable (upstream pool exhausted)",
				TurnErrorKind: proto.TurnErrorTransient,
				TurnErrorCode: proto.TurnErrorUpstreamUnavailable,
			},
		},
		{
			name: "expired_login",
			sess: proto.Session{
				SessionID:     "agent01",
				Status:        proto.StatusDone,
				State:         proto.StateIdle,
				TurnError:     "Not logged in · Please run /login",
				TurnErrorKind: proto.TurnErrorUser,
				TurnErrorCode: proto.TurnErrorNotLoggedIn,
			},
		},
		{
			name: "exhausted_quota",
			sess: proto.Session{
				SessionID:     "agent01",
				Status:        proto.StatusDone,
				State:         proto.StateIdle,
				TurnError:     "You've hit your usage limit",
				TurnErrorKind: proto.TurnErrorUsageLimit,
				TurnErrorCode: proto.TurnErrorRateLimited,
			},
		},
	}

	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", tc.sess)

			// 1. Preview without touching the predecessor.
			prevW := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
				`{"launcher":"codex","preview":true}`, "")
			if prevW.Code != http.StatusOK {
				t.Fatalf("preview = %d %s", prevW.Code, prevW.Body.String())
			}
			if len(e.f.starts) != 0 || len(e.f.stopped) != 0 {
				t.Fatalf("preview mutated sessions: starts=%d stops=%d", len(e.f.starts), len(e.f.stopped))
			}

			// 2. Owner selects codex directly (no response from old model required).
			body := `{"operation_id":"op_first","launcher":"codex","prompt":"Continue after interruption."}`
			w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff", body, "")
			if w.Code != http.StatusOK {
				t.Fatalf("handoff = %d %s", w.Code, w.Body.String())
			}
			var resp struct {
				Handoff TodoContextHandoff `json:"handoff"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Handoff.DeliveryStatus != handoffDeliveryAccepted || resp.Handoff.TargetSession == "" {
				t.Fatalf("handoff not accepted: %+v", resp.Handoff)
			}
			if !strings.Contains(resp.Handoff.Summary, tc.sess.TurnError) {
				t.Fatalf("summary did not record why predecessor stopped (%q):\n%s", tc.sess.TurnError, resp.Handoff.Summary)
			}

			// 3. Regenerating creates a new artifact superseding the first and preserving both for audit.
			e.sessions("box", proto.Session{
				SessionID:      resp.Handoff.TargetSession,
				MachineID:      "box",
				Cwd:            "/code/app",
				Agent:          "codex",
				Launcher:       "codex",
				PermissionMode: "default",
				Status:         proto.StatusIdle,
				State:          proto.StateIdle,
			})
			w2 := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
				`{"operation_id":"op_second","launcher":"profiles-ag","regenerate":true}`, "")
			if w2.Code != http.StatusOK {
				t.Fatalf("regenerate handoff = %d %s", w2.Code, w2.Body.String())
			}
			var resp2 struct {
				Handoff TodoContextHandoff `json:"handoff"`
			}
			if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
				t.Fatal(err)
			}
			if resp2.Handoff.SupersedesID != resp.Handoff.ID {
				t.Fatalf("supersedes_id = %q, want %q", resp2.Handoff.SupersedesID, resp.Handoff.ID)
			}
			listW := todoHandoffHTTPReq(t, e.s, "GET", "/api/v1/todos/"+todo.ID+"/handoffs", "", "")
			var listed struct {
				Handoffs []TodoContextHandoff `json:"handoffs"`
			}
			_ = json.Unmarshal(listW.Body.Bytes(), &listed)
			if len(listed.Handoffs) != 2 {
				t.Fatalf("expected 2 preserved handoffs for audit, got %d", len(listed.Handoffs))
			}
		})
	}
}

// Acceptance Criterion A2 (part 2):
// A safety refusal or unresolved approval stays visible. A switch never grants approval,
// raises permissions, or replays the blocked action. Defer if the old execution cannot safely
// relinquish the workspace.
func TestTodoContextHandoffSafetyRefusalApprovalPermissionAndQuiescence(t *testing.T) {
	t.Run("safety_refusal_blocks_every_switch_path", func(t *testing.T) {
		safetySess := proto.Session{
			SessionID:     "agent01",
			Status:        proto.StatusDone,
			State:         proto.StateIdle,
			TurnError:     "The model's safeguards flagged this request.",
			TurnErrorKind: proto.TurnErrorUser,
			ModelFallback: &proto.ModelRefusal{
				Subtype:       "model_refusal_fallback",
				OriginalModel: "claude-opus",
				Category:      proto.ModelRecoveryReasonSafeguards,
				Explanation:   "the model's safety classifier refused the request",
			},
		}
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", safetySess)

		// 1. Direct owner handoff refused with 409 safety_refusal.
		w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_safety","launcher":"codex"}`, "")
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "safety_refusal") {
			t.Fatalf("expected 409 safety_refusal, got %d %s", w.Code, w.Body.String())
		}

		// 2. task_plan.continues refused before creating a plan card.
		args, _ := json.Marshal(map[string]any{
			"tasks": []map[string]any{{
				"title":         "Try switch after refusal",
				"prompt":        "Continue on codex",
				"machine":       "box",
				"cwd":           "/code/app",
				"launcher":      "codex",
				"role":          "worker",
				"continues":     todo.ID,
				"continues_why": "Attempt switch",
			}},
		})
		if _, err := e.s.toolTaskPlan(context.Background(), "m1", args); err == nil || !strings.Contains(err.Error(), "safety refusal") {
			t.Fatalf("task_plan.continues did not block safety refusal: %v", err)
		}

		// 3. agent_switch and agents_switch refuse safety-refused sessions too.
		ft, _ := switchRig(t)
		safetyRow := agentRow("s1safe0001", "sub", proto.StatusIdle)
		safetyRow.ModelFallback = safetySess.ModelFallback
		safetyRow.TurnError = safetySess.TurnError
		ft.sessions("mac", safetyRow)
		if _, err := switchTool(ft, map[string]any{"machine": "mac", "session_id": "s1safe0001", "launcher": "work", "text": "go on"}); err == nil || !strings.Contains(err.Error(), "safety refusal") {
			t.Fatalf("agent_switch did not block safety refusal: %v", err)
		}
		batchRes, err := batchSwitch(ft, map[string]any{"launcher": "work", "agents": []map[string]any{{"machine": "mac", "session_id": "s1safe0001"}}})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(batchRes, "refused, nothing changed") || !strings.Contains(batchRes, "safety refusal") {
			t.Fatalf("agents_switch did not refuse safety refusal:\n%s", batchRes)
		}
		if len(nodeCalls(ft.f)) != 0 {
			t.Fatalf("safety refusal triggered node calls: %+v", nodeCalls(ft.f))
		}

		// Predecessor was never stopped and no replacement started.
		if len(e.f.stopped) != 0 || len(e.f.starts) != 0 {
			t.Fatalf("safety refusal triggered stop/start: stops=%d starts=%d", len(e.f.stopped), len(e.f.starts))
		}
	})

	t.Run("unresolved_approval_stays_visible_and_blocks_switch", func(t *testing.T) {
		approvalSess := proto.Session{
			SessionID:           "agent01",
			Status:              proto.StatusBlocked,
			State:               proto.StateNeedsYou,
			PendingInteractions: 1,
		}
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", approvalSess)

		w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_approval","launcher":"codex"}`, "")
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "unresolved_approval") {
			t.Fatalf("expected 409 unresolved_approval, got %d %s", w.Code, w.Body.String())
		}
		if len(e.f.stopped) != 0 || len(e.f.starts) != 0 {
			t.Fatalf("unresolved approval switch mutated sessions: stops=%d starts=%d", len(e.f.stopped), len(e.f.starts))
		}

		// agent_switch also refuses when an approval is pending.
		ft, _ := switchRig(t)
		pendingRow := agentRow("s1wait0001", "sub", proto.StatusBlocked)
		pendingRow.PendingInteractions = 1
		ft.sessions("mac", pendingRow)
		if _, err := switchTool(ft, map[string]any{"machine": "mac", "session_id": "s1wait0001", "launcher": "work", "text": "go on"}); err == nil || !strings.Contains(err.Error(), "unresolved approval") {
			t.Fatalf("agent_switch did not block unresolved approval: %v", err)
		}
	})

	t.Run("permission_escalation_refused", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusIdle,
			State:     proto.StateIdle,
		})
		w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_esc","launcher":"codex","permission":"bypassPermissions"}`, "")
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "permission_escalation") {
			t.Fatalf("expected 409 permission_escalation, got %d %s", w.Code, w.Body.String())
		}
		if len(e.f.starts) != 0 || len(e.f.stopped) != 0 {
			t.Fatalf("permission escalation mutated sessions: stops=%d starts=%d", len(e.f.stopped), len(e.f.starts))
		}
	})

	t.Run("workspace_not_quiescent_defers", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusWorking,
			State:     proto.StateWorking,
		})
		e.f.retireErr = errors.New("pane busy: cannot stop cleanly")
		w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_defer","launcher":"codex"}`, "")
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "workspace_not_quiescent") {
			t.Fatalf("expected 409 workspace_not_quiescent, got %d %s", w.Code, w.Body.String())
		}
		if len(e.f.starts) != 0 || todoNow(e.s, todo.ID).Agents[0].ReleasedAt != nil {
			t.Fatalf("non-quiescent workspace started replacement or released predecessor")
		}
	})
}

// Acceptance Criterion A3:
// Target startup rejection preserves recoverable task state. Retrying the same operation or racing
// two requests cannot start two active workers or inject context twice. Unknown delivery remains
// pending. Cover missing transcript/diff and legacy-client behavior.
func TestTodoContextHandoffRejectionIdempotencyConcurrencyUnknownAndGaps(t *testing.T) {
	t.Run("target_rejection_preserves_recoverable_state_and_allows_next_target", func(t *testing.T) {
		e, todo, brief := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusDone,
			State:     proto.StateIdle,
		})
		e.f.failStart = &rpc.Error{Code: proto.CodeLauncherMissing, Message: "launcher codex is missing"}
		w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_reject","launcher":"codex"}`, "")
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "target_start_rejected") {
			t.Fatalf("expected 409 target_start_rejected, got %d %s", w.Code, w.Body.String())
		}
		var rejected struct {
			Handoff TodoContextHandoff `json:"handoff"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &rejected)
		if rejected.Handoff.DeliveryStatus != handoffDeliveryRejected || !strings.Contains(rejected.Handoff.DeliveryError, "codex is missing") {
			t.Fatalf("rejected handoff metadata mismatch: %+v", rejected.Handoff)
		}
		// To-do stays open and recoverable with brief and historical link preserved.
		cur := todoNow(e.s, todo.ID)
		if cur.DoneAt != nil || cur.Cancelled() {
			t.Fatalf("target rejection closed to-do: %+v", cur)
		}
		if kept, ok := e.s.briefOfTodo(todo.ID); !ok || kept.ID != brief.ID {
			t.Fatalf("target rejection lost brief: %+v", kept)
		}
		// Owner can recover immediately by selecting another eligible target.
		e.f.failStart = nil
		w2 := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_recover","launcher":"profiles-ag"}`, "")
		if w2.Code != http.StatusOK {
			t.Fatalf("recovery handoff failed: %d %s", w2.Code, w2.Body.String())
		}
		var recovered struct {
			Handoff TodoContextHandoff `json:"handoff"`
		}
		_ = json.Unmarshal(w2.Body.Bytes(), &recovered)
		if recovered.Handoff.DeliveryStatus != handoffDeliveryAccepted || recovered.Handoff.SupersedesID != rejected.Handoff.ID {
			t.Fatalf("recovery handoff mismatch: %+v", recovered.Handoff)
		}
	})

	t.Run("same_operation_retry_is_idempotent", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusDone,
			State:     proto.StateIdle,
		})
		body := `{"operation_id":"op_idem_1","launcher":"codex"}`
		w1 := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff", body, "")
		w2 := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff", body, "")
		if w1.Code != http.StatusOK || w2.Code != http.StatusOK {
			t.Fatalf("codes = %d, %d", w1.Code, w2.Code)
		}
		var r1, r2 struct {
			Handoff TodoContextHandoff `json:"handoff"`
		}
		_ = json.Unmarshal(w1.Body.Bytes(), &r1)
		_ = json.Unmarshal(w2.Body.Bytes(), &r2)
		if r1.Handoff.ID != r2.Handoff.ID || len(e.f.starts) != 1 {
			t.Fatalf("idempotent retry duplicated work: id1=%s id2=%s starts=%d", r1.Handoff.ID, r2.Handoff.ID, len(e.f.starts))
		}
	})

	t.Run("concurrent_racing_requests_start_at_most_one_worker", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusDone,
			State:     proto.StateIdle,
		})
		entered := make(chan struct{}, 1)
		release := make(chan struct{})
		setHub(e.s, &blockingStartHub{Hub: e.f, entered: entered, release: release})

		var wg sync.WaitGroup
		var w1, w2 *httptest.ResponseRecorder
		wg.Add(1)
		go func() {
			defer wg.Done()
			w1 = todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
				`{"operation_id":"op_race_a","launcher":"codex"}`, "")
		}()
		<-entered
		w2 = todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_race_b","launcher":"profiles-ag"}`, "")
		close(release)
		wg.Wait()

		if w1.Code != http.StatusOK {
			t.Fatalf("winner failed: %d %s", w1.Code, w1.Body.String())
		}
		if w2.Code != http.StatusConflict || !strings.Contains(w2.Body.String(), "handoff_in_progress") {
			t.Fatalf("racing request was not serialized: %d %s", w2.Code, w2.Body.String())
		}
		e.f.mu.Lock()
		startsCount := len(e.f.starts)
		e.f.mu.Unlock()
		if startsCount != 1 {
			t.Fatalf("race started %d workers, want 1", startsCount)
		}
	})

	t.Run("unknown_delivery_remains_pending_and_never_replays", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusDone,
			State:     proto.StateIdle,
		})
		oldWait, oldPoll := taskStartAdoptWait, taskStartAdoptPoll
		taskStartAdoptWait, taskStartAdoptPoll = 5*time.Millisecond, time.Millisecond
		defer func() { taskStartAdoptWait, taskStartAdoptPoll = oldWait, oldPoll }()

		e.f.failStart = context.DeadlineExceeded
		w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_unknown","launcher":"codex"}`, "")
		if w.Code != http.StatusAccepted {
			t.Fatalf("expected 202 for unknown delivery, got %d %s", w.Code, w.Body.String())
		}
		var unknown struct {
			Handoff TodoContextHandoff `json:"handoff"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &unknown)
		if unknown.Handoff.DeliveryStatus != handoffDeliveryUnknown {
			t.Fatalf("expected delivery_status=unknown, got %+v", unknown.Handoff)
		}
		e.f.mu.Lock()
		startsBeforeRetry := len(e.f.starts)
		e.f.mu.Unlock()
		// Retrying the same operation_id returns the existing unknown handoff and does NOT replay.
		e.f.failStart = nil
		wRetry := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_unknown","launcher":"codex"}`, "")
		if wRetry.Code != http.StatusOK {
			t.Fatalf("retry of unknown op = %d %s", wRetry.Code, wRetry.Body.String())
		}
		var retried struct {
			Handoff TodoContextHandoff `json:"handoff"`
		}
		_ = json.Unmarshal(wRetry.Body.Bytes(), &retried)
		if retried.Handoff.ID != unknown.Handoff.ID || retried.Handoff.DeliveryStatus != handoffDeliveryUnknown {
			t.Fatalf("unknown op replayed or mutated: %+v", retried.Handoff)
		}
		e.f.mu.Lock()
		startsAfterRetry := len(e.f.starts)
		e.f.mu.Unlock()
		if startsAfterRetry != startsBeforeRetry {
			t.Fatalf("unknown delivery replayed start: before=%d after=%d", startsBeforeRetry, startsAfterRetry)
		}
	})

	t.Run("missing_transcript_and_diff_gaps_with_defer_option", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusDone,
			State:     proto.StateIdle,
		})
		e.f.nodes[DeviceSessionItems] = func(_ string, _ json.RawMessage) (any, error) {
			return nil, errors.New("transcript file not found")
		}
		e.f.nodes["git.review"] = func(_ string, _ json.RawMessage) (any, error) {
			return nil, &rpc.Error{Code: proto.CodeNotGit, Message: "not a Git repository"}
		}

		// With defer_on_missing_sources=true, defers before stopping predecessor.
		deferW := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_gaps_defer","launcher":"codex","defer_on_missing_sources":true}`, "")
		if deferW.Code != http.StatusConflict || !strings.Contains(deferW.Body.String(), "missing_sources") {
			t.Fatalf("expected 409 missing_sources, got %d %s", deferW.Code, deferW.Body.String())
		}
		if len(e.f.stopped) != 0 || len(e.f.starts) != 0 {
			t.Fatalf("deferred handoff mutated sessions")
		}

		// Without defer_on_missing_sources, proceeds and labels both gaps explicitly.
		goW := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
			`{"operation_id":"op_gaps_go","launcher":"codex"}`, "")
		if goW.Code != http.StatusOK {
			t.Fatalf("handoff with gaps = %d %s", goW.Code, goW.Body.String())
		}
		var got struct {
			Handoff TodoContextHandoff `json:"handoff"`
		}
		_ = json.Unmarshal(goW.Body.Bytes(), &got)
		if len(got.Handoff.MissingSources) < 2 {
			t.Fatalf("expected transcript and git_diff gaps, got %+v", got.Handoff.MissingSources)
		}
		if !strings.Contains(got.Handoff.Summary, "transcript:") || !strings.Contains(got.Handoff.Summary, "git_diff:") {
			t.Fatalf("summary did not label missing sources:\n%s", got.Handoff.Summary)
		}
	})

	t.Run("legacy_client_decodes_detail_and_starts_legacy_plan", func(t *testing.T) {
		e, todo, _ := setupHandoffFixture(t, nil, "claude", "claude", "default", proto.Session{
			SessionID: "agent01",
			Status:    proto.StatusDone,
			State:     proto.StateIdle,
		})
		// Legacy plan saved without any handoff fields still starts via existing endpoint.
		legacy := &TaskPlan{
			ID: "plan_legacy_1", Conversation: "m1", State: planOpen,
			Tasks: []PlanTask{{
				Title: "Legacy continue", Prompt: "Keep going", Machine: "box", Cwd: "/code/app",
				Launcher: "codex", Permission: "default", Continues: todo.ID, ContinuesWhy: "Legacy flow",
			}},
		}
		if err := e.s.savePlan(context.Background(), legacy); err != nil {
			t.Fatal(err)
		}
		startW := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/task-plans/"+legacy.ID+"/start", `{}`, "")
		if startW.Code != http.StatusOK {
			t.Fatalf("legacy plan start = %d %s", startW.Code, startW.Body.String())
		}
		waitFor(t, "legacy continuation agent linked", func() bool {
			return len(todoNow(e.s, todo.ID).Agents) == 2 && e.s.taskStarting.Load() == 0
		})
		// Legacy client ignoring unknown fields decodes GET /api/v1/todos/{id} cleanly.
		detailW := todoHandoffHTTPReq(t, e.s, "GET", "/api/v1/todos/"+todo.ID, "", "")
		var legacyClient struct {
			Todo json.RawMessage `json:"todo"`
		}
		if err := json.NewDecoder(bytes.NewReader(detailW.Body.Bytes())).Decode(&legacyClient); err != nil || len(legacyClient.Todo) == 0 {
			t.Fatalf("legacy client failed to decode detail: err=%v body=%s", err, detailW.Body.String())
		}
	})
}

func TestTodoContextHandoffPostgresPersistence(t *testing.T) {
	database := taskRecoveryDatabase(t)
	e, todo, _ := setupHandoffFixture(t, database, "claude", "claude", "default", proto.Session{
		SessionID: "agent01",
		Status:    proto.StatusDone,
		State:     proto.StateIdle,
	})
	w := todoHandoffHTTPReq(t, e.s, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
		`{"operation_id":"op_pg_1","launcher":"codex","prompt":"Continue with Postgres persistence."}`, "")
	if w.Code != http.StatusOK {
		t.Fatalf("handoff = %d %s", w.Code, w.Body.String())
	}
	var got struct {
		Handoff TodoContextHandoff `json:"handoff"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	// Reload in a brand new Service backed by the same database: handoff survives restarts
	// and preserves idempotency for op_pg_1.
	restarted := newServiceOn(t, e.f, database)
	listW := todoHandoffHTTPReq(t, restarted, "GET", "/api/v1/todos/"+todo.ID+"/handoffs", "", "")
	if listW.Code != http.StatusOK {
		t.Fatalf("restarted GET handoffs = %d %s", listW.Code, listW.Body.String())
	}
	var listed struct {
		Handoffs []TodoContextHandoff `json:"handoffs"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &listed); err != nil || len(listed.Handoffs) != 1 {
		t.Fatalf("restarted handoffs mismatch: %+v err=%v", listed, err)
	}
	if listed.Handoffs[0].ID != got.Handoff.ID || listed.Handoffs[0].DeliveryStatus != handoffDeliveryAccepted ||
		listed.Handoffs[0].Summary != got.Handoff.Summary {
		t.Fatalf("restarted handoff did not preserve exact fields: %+v", listed.Handoffs[0])
	}
	if !strings.Contains(listed.Handoffs[0].Summary, "Single-flight per token key") {
		t.Fatalf("persisted handoff summary missing recorded decision:\n%s", listed.Handoffs[0].Summary)
	}
	// Retrying op_pg_1 after restart stays strictly idempotent via the Postgres unique index.
	startsBefore := len(e.f.starts)
	retryW := todoHandoffHTTPReq(t, restarted, "POST", "/api/v1/todos/"+todo.ID+"/handoff",
		`{"operation_id":"op_pg_1","launcher":"codex","prompt":"Continue with Postgres persistence."}`, "")
	if retryW.Code != http.StatusOK {
		t.Fatalf("restarted idempotent retry = %d %s", retryW.Code, retryW.Body.String())
	}
	var retried struct {
		Handoff TodoContextHandoff `json:"handoff"`
	}
	if err := json.Unmarshal(retryW.Body.Bytes(), &retried); err != nil || retried.Handoff.ID != got.Handoff.ID {
		t.Fatalf("restarted retry created new handoff: %+v err=%v", retried, err)
	}
	if len(e.f.starts) != startsBefore {
		t.Fatalf("restarted retry started another worker: before=%d after=%d", startsBefore, len(e.f.starts))
	}
}
