# Main-chat model selection screenshots (#7857)

Synthetic data only. Captured by `apple/PersonaUITests/AssistantModelPickerUITests.swift` against
`scripts/mock/mock_hub.py` started with `MOCK_MODEL_PICK=1`; no real hub, account or model provider is involved.

- Build: Debug build of `origin/main`, Xcode 26.6, iPhone 17 Pro simulator on iOS 26.5, app language zh-Hans (the test
  suite's language), default text size. Appearance set with `-ui.scheme light|dark`.
- Flow: open the main Persona conversation, open the composer menu's **Change Model…** sheet, pick Haiku 5.5. The composer
  button names the effective model by alias (Opus → Haiku); the sheet's catalog rows carry the full names (Opus 5.5 ticked,
  Haiku 5.5 picked). The mock records `PUT /api/v1/assistant/conversations/<id>/model` with
  `{"launcher": "claude", "model": "haiku"}`, readable at `GET /mock/model_pick`.

Files: `model-picker-<1-entry|2-menu|3-sheet|4-picked>-<light|dark>.png`.
