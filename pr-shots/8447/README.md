# Android Board large-text evidence

Current fix and after-capture source: **`f4b6df3e84e9899763518b5b4639664c5638ffee`** (short form `f4b6df3e84`). This directory now contains the complete 12-image matrix: four Board **before** PNGs and eight **after** PNGs. All 12 final PNGs were opened individually and visually reviewed. Within the native Robolectric evidence scope, the acceptance checks pass; the remaining provenance and clock limitations are stated below.

## Before-image provenance and validity

The four before images are byte-for-byte copies of the closed-state Board captures in `../7267-section-header/`. Their recorded provenance is the original 66-image capture set documented in `../README.md`:

- Recorded source: main commit **`aa5e1ec43e49a46609ada1da4906d4cf41e04db6`**, which contains the #7308 squash commit `074c245c6b3c72ea6f4725d5848abeb75b4da31c`. The capture record says no source, fixture, or documentation files were edited.
- Recorded environment: `source /root/android-tools-v9/env.sh`, `/root/.cargo/bin` on `PATH`, and Gradle serialized with `/tmp/inbox-matters-android-gradle.lock`.
- The original capture used `--rerun-tasks` and `-PpersonaApplicationId=com.example.persona` while running the existing Inbox showcase, section-header, line-section, and Say Something screenshot tests.
- These are native JVM Compose UI captures under Robolectric (`sdk = 35`, `GraphicsMode.Mode.NATIVE`), not emulator or physical-device screenshots. The test uses English, a 400 dp × 1000 dp page at xhdpi density, Reduce Motion, and 800 × 2000 pixel PNG output.
- `1.0x` and `2.0x` mean 100% and 200% **font scale**. They do not mean a change from 1x to 2x display density.

### Source-drift audit from the recorded before commit to `71c614659c`

`aa5e1ec43e49a46609ada1da4906d4cf41e04db6` is an ancestor of `71c614659c`. The whole source trees are not identical, so this is not a fresh same-baseline before recapture. The focused diff audit establishes the following for the Board-top and section-header evidence:

- `android/core/designsystem/src/main/kotlin/persona/core/designsystem/Page.kt`, including `JListPage`, `JTopBar`, and the Board summary path, is unchanged.
- `Agents.kt` differs only in `AgentRow`; `QuickFilterBar`, including its Board cells, is unchanged.
- `InboxScreen.kt` differs in the source-subscriptions action and filtered-empty state. Its summary, `JListPage` call, quick filters, search/grouping controls, and section-header call are unchanged. The English string changes are likewise confined to filtered-empty text and Inbox Sources.
- `Lists.kt` differs only in `JRow`; `JSectionHeader` is unchanged.
- The closed-state `headersKeepTheirTitlesInEveryAppearance` capture loop and `capture` function in `InboxSectionHeaderShotsTest.kt` are unchanged. The later test diff adds the independent open-menu cases and their helper.

On that targeted audit, the four copied PNGs are traceable before evidence for this focused Board-top comparison. The remaining limitation is explicit: they were captured at the recorded older main commit, not recaptured at `71c614659c`, and their commit provenance comes from the adjacent capture record rather than PNG metadata. A freshly captured same-baseline control was not part of this evidence run and remains unavailable; the comparisons below therefore use these audited older-main before images.

## After-capture command and result

The existing screenshot class was run once from **`/root/jarvis-work/jobs/inbox-matters/inbox-matters-android-board-large-text/android/`** at source commit **`f4b6df3e84e9899763518b5b4639664c5638ffee`**, after `source /root/android-tools-v9/env.sh` and `export PATH=/root/.cargo/bin:$PATH`:

```sh
flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxSectionHeaderShotsTest'
```

The invocation exited **0** with **`BUILD SUCCESSFUL in 15s`** and **`103 actionable tasks: 1 executed, 102 up-to-date`**. The test wrote 12 closed-state and six open-menu PNGs to:

```text
/root/jarvis-work/jobs/inbox-matters/inbox-matters-android-board-large-text/android/feature/inbox/build/inbox-section-header-shots/
```

Only the eight required closed-state after images listed below were copied into this directory, with the `-after` suffix shown in the matrix. `cmp` verified that every copied after PNG is byte-identical to its corresponding raw capture.

### Native rendering and review limits

These after images are native JVM Compose UI captures under Robolectric (`sdk = 35`, `GraphicsMode.Mode.NATIVE`), not emulator or physical-device screenshots. The class uses English qualifiers `en-w400dp-h1000dp-xhdpi`, a 400 dp × 1000 dp page at unchanged xhdpi density, Reduce Motion, and 800 × 2000 pixel PNG output. `1.0x` and `2.0x` mean 100% and 200% **font scale**, not a display-density change.

All 12 final PNGs were opened one by one with an image viewer and received Agent visual review. No human, emulator, physical-device, TalkBack or broader accessibility review is claimed. Native Robolectric rendering does not establish device rendering or accessibility behavior.

The closed-state `capture` path does not install the fixed `LocalInboxClock` used by `menuOpenShot`. Consequently, the fixture's `2026-11-13T10:00:00Z` timestamp appears as `02:00` in the November 13 before/reference images and as `Fri` in the November 15 after images. This calendar rollover is the only pixel difference in the Board 100% and Brief/Glass 200% control comparisons below.

### Pixel comparisons and regression classification

Pillow compared decoded RGB pixels after checking the PNG bytes. Bounds below are `(left, top)-(right, bottom)` in the 800 × 2000 images, with right and bottom exclusive. Each pair has two regions, one for each event's timestamp.

| Comparison | Bytes | Changed pixels | Difference regions | Classification |
| --- | --- | ---: | --- | --- |
| Board light 100%, before vs after | Different | 1,340 | `(693,649)-(751,668)`; `(693,797)-(751,816)` | Only `02:00` → `Fri`; all other pixels identical |
| Board dark 100%, before vs after | Different | 1,340 | `(693,649)-(751,668)`; `(693,797)-(751,816)` | Only `02:00` → `Fri`; all other pixels identical |
| Brief light 200%, existing reference vs after | Different | 4,098 | `(151,1056)-(280,1093)`; `(151,1419)-(280,1456)` | Only `02:00` → `Fri`; all other pixels identical |
| Brief dark 200%, existing reference vs after | Different | 4,082 | `(151,1056)-(280,1093)`; `(151,1419)-(280,1456)` | Only `02:00` → `Fri`; all other pixels identical |
| Glass light 200%, existing reference vs after | Different | 3,916 | `(174,986)-(290,1022)`; `(174,1337)-(290,1373)` | Only `02:00` → `Fri`; all other pixels identical |
| Glass dark 200%, existing reference vs after | Different | 3,916 | `(174,986)-(290,1022)`; `(174,1337)-(290,1373)` | Only `02:00` → `Fri`; all other pixels identical |

Thus none of these six control pairs is byte- or pixel-identical, but every changed pixel is confined to the two clock labels. There is no visual or pixel evidence of a PR-caused regression in Board 100% or Brief/Glass 200%; their header, section and layout pixels are unchanged. The Brief/Glass references are the existing files under `../7267-section-header/` named in the comparison request.

### Visual review and acceptance

- **Board light and dark 200%: pass.** The `Inbox` title is fully visible. The summary line is below the title and above the statistics cards, with no vertical clipping or overlap. The summary is horizontally ellipsized as `2 to answer · Persona is…`; the statistics' `Needs …` chip also remains ellipsized, as before. The fix moves the statistics and following content down to clear the larger header.
- **Board light and dark 100%: pass.** The header and page layout are unchanged from before; only the two timestamp labels differ because of the clock rollover.
- **Brief and Glass, light and dark 200%: pass.** Their layouts are unchanged from the existing references except for the same two timestamp labels. No new overlap or clipping is visible.
- **Section heading and overflow: pass.** In every 200% after image, `Needs you 2` and the section `⋯` are visible, separated and aligned without collision. Board 100% retains the inline `Collapse` and `Mark all as read` actions. The other page-level `⋯` controls also remain visible.
- **Before images: reviewed as baselines.** Board light/dark 100% are intact controls. Board light/dark 200% show the original summary-to-statistics overlap while the section heading and overflow remain visible.

### Existing test's menu and overflow coverage

The source of **`persona.feature.inbox.InboxSectionHeaderShotsTest`** confirms the coverage exercised by the successful run. `headersKeepTheirTitlesInEveryAppearance` iterates every style, light/dark and 100%/200% text, asserts one `Needs you` heading, and asserts `inbox.section.overflow` at 200%; its Brief-light 200% finish opens that control and checks `Mark all as read` and `Collapse`.

The six dedicated tests **`boardLightMenuOpenAt200PercentText`**, **`boardDarkMenuOpenAt200PercentText`**, **`briefLightMenuOpenAt200PercentText`**, **`briefDarkMenuOpenAt200PercentText`**, **`glassLightMenuOpenAt200PercentText`** and **`glassDarkMenuOpenAt200PercentText`** call `menuOpenShot`. That helper asserts the overflow is displayed, clicks it, requires a real popup window, and asserts both `Mark all as read` and `Collapse` are displayed before writing each open-menu PNG. Those six open-menu images were generated in the raw output directory but are not part of this directory's required closed-state matrix.

## Required 12-image matrix

Before raw paths are under `../7267-section-header/`; after raw paths are under the generated output directory above.

| Final filename in this directory | Role | Raw PNG filename | Status |
| --- | --- | --- | --- |
| `board-light-1.0x-before.png` | Board light, 100% text, before | `board-light-1.0x.png` | Present and visually reviewed; intact baseline |
| `board-light-2.0x-before.png` | Board light, 200% text, before | `board-light-2.0x.png` | Present and visually reviewed; original summary/statistics overlap visible |
| `board-dark-1.0x-before.png` | Board dark, 100% text, before | `board-dark-1.0x.png` | Present and visually reviewed; intact baseline |
| `board-dark-2.0x-before.png` | Board dark, 200% text, before | `board-dark-2.0x.png` | Present and visually reviewed; original summary/statistics overlap visible |
| `board-light-1.0x-after.png` | Board light, 100% text, after | `board-light-1.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, layout unchanged except clock labels |
| `board-light-2.0x-after.png` | Board light, 200% text, after | `board-light-2.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, title and summary clear statistics |
| `board-dark-1.0x-after.png` | Board dark, 100% text, after | `board-dark-1.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, layout unchanged except clock labels |
| `board-dark-2.0x-after.png` | Board dark, 200% text, after | `board-dark-2.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, title and summary clear statistics |
| `brief-light-2.0x-after.png` | Brief light, 200% text, after | `brief-light-2.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, unchanged except clock labels |
| `brief-dark-2.0x-after.png` | Brief dark, 200% text, after | `brief-dark-2.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, unchanged except clock labels |
| `glass-light-2.0x-after.png` | Glass light, 200% text, after | `glass-light-2.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, unchanged except clock labels |
| `glass-dark-2.0x-after.png` | Glass dark, 200% text, after | `glass-dark-2.0x.png` | Captured at `f4b6df3e84` and reviewed; pass, unchanged except clock labels |
