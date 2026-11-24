package assistant

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// taskHandoff is rendered after placement and launcher fitting. An optional node.info read must never turn a
// usable assignment into a failed or indefinitely waiting start merely because its handoff hint is unavailable.
func (s *Service) taskHandoff(ctx context.Context, machine string, fit startAgent) string {
	return prompts.RenderTaskHandoff(s.taskEntry(ctx, machine, fit))
}

// isTerminalLauncherSource reports whether a launcher's provenance represents a terminal pane rather than a runtime
// or desktop app. Empty is a legacy shell launcher (proto.Launcher).
func isTerminalLauncherSource(source, agent string) bool {
	source = compat.Source(source)
	if source == "persona" && agent == "agy" {
		return false // managed agy starts on its runtime (node/runtimes_start.go), not a pane
	}
	return source == "" || source == "zshrc" || source == "path" || source == "persona" || source == "builtin" && agent == "claude"
}

// taskEntry is the verified way the fitted Agent reaches its own Persona tools: the MCP tools, the terminal entry
// (the node's executable), or none. The handoff note names it, and a task leader's note follows it (#12136).
func (s *Service) taskEntry(ctx context.Context, machine string, fit startAgent) prompts.TaskHandoff {
	source := compat.Source(fit.source)
	// A runtime or app's same-family agent does not establish terminal caller identity.
	// Paseo injects the existing Persona MCP server into its Claude and Codex providers.
	terminal := isTerminalLauncherSource(source, fit.agent)
	mcp := terminal && fit.agent == "claude" || source == proto.EnginePaseo && (fit.agent == "claude" || fit.agent == "codex")
	return s.taskEntryFor(ctx, machine, fit.agent, mcp, terminal)
}

// adoptedTaskEntry uses the running session's provenance. A Codex app-server thread
// can identify its caller through CODEX_THREAD_ID even when it has no terminal pane.
func (s *Service) adoptedTaskEntry(ctx context.Context, machine string, c proto.Session) prompts.TaskHandoff {
	paseo := c.Host == proto.EnginePaseo
	terminal := !proto.RuntimeEngine(c.Host) && (c.SourceKind == "" || c.SourceKind == proto.SourceClaudeTerminal ||
		c.Agent == "codex" && c.SourceThreadID != "" && (c.SourceKind == "codex_app_server" || c.SourceKind == "codex_history"))
	mcp := terminal && c.Agent == "claude" || paseo && (c.Agent == "claude" || c.Agent == "codex")
	return s.taskEntryFor(ctx, machine, c.Agent, mcp, terminal)
}

func (s *Service) taskEntryFor(ctx context.Context, machine, agent string, mcp, terminal bool) prompts.TaskHandoff {
	h := prompts.TaskHandoff{Language: answerLanguage(s.hello(ctx).Locale), MCP: mcp}
	if !mcp && terminal && (agent == "codex" || proto.IsWorkAgentType(agent) || proto.IsNoModeAgentType(agent)) {
		h.Executable = s.nodeExecutableJSON(ctx, machine)
	}
	return h
}

// nodeExecutableJSON queries machine's node.info within a short bound and returns its verified absolute executable
// encoded as a JSON string, never shell code. Returns "" on timeout, error, or a non-absolute path (#9846, #13893).
func (s *Service) nodeExecutableJSON(ctx context.Context, machine string) string {
	if machine == "" {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, min(agentStartListTimeout, time.Second))
	defer cancel()
	var info proto.NodeInfo
	if s.call(cctx, machine, "node.info", map[string]any{}, &info) == nil && proto.NodeAbs(info.Executable) {
		// Keep paths, including quotes and line breaks, as JSON data rather than constructing a shell command.
		encoded, _ := json.Marshal(info.Executable)
		return string(encoded)
	}
	return ""
}
