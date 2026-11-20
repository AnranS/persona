package voice

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
)

const modeDictation = "dictation"

// startDictation starts one unbound, text-only session. A service that refuses output_modalities gets one replacement
// without it, as meeting transcription does; no other failure opens another session.
func (c *conversation) startDictation(ctx context.Context, up upLink, p *Provider, w wire, model, instructions string) (int, upLink, error) {
	textOnly := !trMemoOf(p).noTextOnly.Load()
	for {
		s := session{Model: model, InputRate: p.inputRate(), Instructions: instructions, Dictation: true, TextOnly: textOnly}
		rate, err := startUpstream(ctx, up, w, p, s)
		if err == nil {
			return rate, up, nil
		}
		var refused *sessionRefused
		if !textOnly || !errors.As(err, &refused) || upstreamCode(err) != "" || refusedField(refused, false) != "text_only" {
			return 0, up, err
		}
		if trMemoOf(p).noTextOnly.CompareAndSwap(false, true) {
			c.logf("provider %s: the service refused text-only output (%s): dictation output audio is discarded, but billed", p.ID,
				firstNonEmpty(refused.code, "no code"))
		}
		textOnly = false
		up, err = c.dial(ctx, p, w, model)
		if err != nil {
			return 0, nil, err
		}
	}
}

// dictationEvents adds the upstream item id and final flag of user captions to the call wire's events. Interim captions
// are whole replacements; standard input transcript deltas are appended. Every other frame keeps its ordinary decoding,
// so handleDictation can explicitly discard generated output.
func dictationEvents(w wire, raw []byte) []event {
	var f struct {
		Type    string `json:"type"`
		ItemID  string `json:"item_id"`
		Delta   string `json:"delta"`
		Text    string `json:"text"`
		IsFinal bool   `json:"is_final"`
	}
	if json.Unmarshal(raw, &f) == nil {
		switch {
		case strings.HasSuffix(f.Type, trCaptionSuffix):
			return []event{{kind: evUser, id: f.ItemID, text: f.Text, replace: true, final: f.IsFinal}}
		case f.Type == "session.input_transcript.delta":
			return []event{{kind: evUser, id: f.ItemID, text: f.Delta, final: f.IsFinal}}
		}
	}
	return w.decode(raw)
}

// handleDictation forwards only the user's words. Generated text, audio, reply boundaries, interruptions, delegations
// and function calls have no client or conversation effect (mu held).
func (c *conversation) handleDictation(gen int, e event) {
	switch e.kind {
	case evUser:
		now := time.Now()
		if e.id != "" && c.user != nil && c.user.id != e.id {
			c.closeUser()
		}
		spoke := c.spokeAt(now)
		if strings.TrimSpace(e.text) != "" {
			c.gate.user = now
			c.gate.caption, c.userAt = now, now
		}
		c.userTextWithID(e.id, e.text, e.replace, e.final, spoke)
	case evError:
		c.logf("upstream: session error (%s, %s): %s", e.code, firstNonEmpty(e.param, "-"), clipRunes(e.text, errorRunes))
		if e.code == codeUpstreamUnavailable {
			c.down.session, c.down.text = sessionNo(gen), clipRunes(e.text, errorRunes)
		}
	}
}

// dictationLost ends one dictation where its only upstream ended. Unlike a call, there is no reconnect, clear, link or
// tool frame: the client may start a fresh dictation, which gets a fresh upstream session.
func (c *conversation) dictationLost(down bool, text string, why error) {
	if down {
		c.failCode(codeUpstreamUnavailable, i18n.T(c.language(), "voice-upstream.unavailable",
			i18n.Args{"reason": firstNonEmpty(text, codeUpstreamUnavailable)}), true)
		return
	}
	c.failCode(firstNonEmpty(upstreamCode(why), codeUpstreamFailed), "语音连接断了，重连没成功："+why.Error(), true)
}
