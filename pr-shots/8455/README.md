# Expanded reported original: before/after large-text evidence

## Source and scope

- **Before:** baseline main commit **`71c614659c855d924fca9c3511fd6ea05469721c`**.
- **After:** source head **`06f6dd7c7b9480c2aa990f4b40f714265031a3b5`**.
- This directory contains **exactly 24 expanded PNGs: 12 under `before/` and 12 under `after/`**. Each phase covers **100%/200% text × Board/Brief/Glass × light/dark**. There are no collapsed captures in this comparison.
- Capture test: `persona.feature.inbox.InboxReportedShotsTest`, using Robolectric **SDK 35**, **native graphics**, and qualifiers `en-w400dp-h2600dp-xhdpi`. All captures are 400 dp wide at xhdpi density. The checked PNG dimensions are 800 × 1800 pixels at 100% text and 800 × 4800 pixels at 200% text.
- `100pct` and `200pct` mean Compose **`fontScale = 1.0` and `fontScale = 2.0`**, respectively. The 200% runs change text scale through `LocalDensity` while leaving the configured **xhdpi pixel density unchanged**; `200pct` does not mean 2x pixel density.

## Reproduction commands

First source `/root/android-tools-v9/env.sh` and add `/root/.cargo/bin` to `PATH`:

```sh
source /root/android-tools-v9/env.sh
export PATH="/root/.cargo/bin:$PATH"
```

Run each command below from **`android/`** at the corresponding source revision. `JAVA_TOOL_OPTIONS` passes both shot properties to the Robolectric test JVM; the output root is the only difference between the two capture commands.

**Before:**

```sh
export JAVA_TOOL_OPTIONS='-Dpersona.shots=/root/jarvis-work/jobs/inbox-matters/shots/android-review-5/7604-reported-layout/before -Dpersona.shots.phase=expanded' ; flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxReportedShotsTest'
```

**After:**

```sh
export JAVA_TOOL_OPTIONS='-Dpersona.shots=/root/jarvis-work/jobs/inbox-matters/shots/android-review-5/7604-reported-layout/after -Dpersona.shots.phase=expanded' ; flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxReportedShotsTest'
```

## Verified file matrix

All 24 paths below were checked and exist as PNG files; the exact expected set has no missing or extra entries.

| Text | Style | Appearance | Before path | After path |
| --- | --- | --- | --- | --- |
| 100% | Board | Light | `before/reported-expanded-board-light-100pct.png` | `after/reported-expanded-board-light-100pct.png` |
| 100% | Board | Dark | `before/reported-expanded-board-dark-100pct.png` | `after/reported-expanded-board-dark-100pct.png` |
| 100% | Brief | Light | `before/reported-expanded-brief-light-100pct.png` | `after/reported-expanded-brief-light-100pct.png` |
| 100% | Brief | Dark | `before/reported-expanded-brief-dark-100pct.png` | `after/reported-expanded-brief-dark-100pct.png` |
| 100% | Glass | Light | `before/reported-expanded-glass-light-100pct.png` | `after/reported-expanded-glass-light-100pct.png` |
| 100% | Glass | Dark | `before/reported-expanded-glass-dark-100pct.png` | `after/reported-expanded-glass-dark-100pct.png` |
| 200% | Board | Light | `before/reported-expanded-board-light-200pct.png` | `after/reported-expanded-board-light-200pct.png` |
| 200% | Board | Dark | `before/reported-expanded-board-dark-200pct.png` | `after/reported-expanded-board-dark-200pct.png` |
| 200% | Brief | Light | `before/reported-expanded-brief-light-200pct.png` | `after/reported-expanded-brief-light-200pct.png` |
| 200% | Brief | Dark | `before/reported-expanded-brief-dark-200pct.png` | `after/reported-expanded-brief-dark-200pct.png` |
| 200% | Glass | Light | `before/reported-expanded-glass-light-200pct.png` | `after/reported-expanded-glass-light-200pct.png` |
| 200% | Glass | Dark | `before/reported-expanded-glass-dark-200pct.png` | `after/reported-expanded-glass-dark-200pct.png` |

### Unchanged 100% controls

All **six** 100% before/after pairs are SHA-256-identical:

| Filename shared by `before/` and `after/` | SHA-256 |
| --- | --- |
| `reported-expanded-board-dark-100pct.png` | `9d8f43560bada3c699e3149b717aa4375492a831874464ab9d4ba9e140b61c7f` |
| `reported-expanded-board-light-100pct.png` | `2f2245d926bf78d09fbd583c96fd5310e2efbeade0d5b6546e6ec9ab2c085e84` |
| `reported-expanded-brief-dark-100pct.png` | `edb28fa5f0a2fc9ecfffb0acceb9daf588ae1b9fed98f884496f3be903998960` |
| `reported-expanded-brief-light-100pct.png` | `69cd0bce336116d63b63162bb862d83bf169b5d1c83df4f3a8cf5dbf69b8c743` |
| `reported-expanded-glass-dark-100pct.png` | `6a604d1bee2cd41a193802c22ca8aee8552f6930719050dcb068be82878725a1` |
| `reported-expanded-glass-light-100pct.png` | `a6e7c2a7030a8300ed8971310ff66fd075d87fcfcb90d33320242fe9186bc0bd` |

## Measured geometry

The baseline's expanded **200%** measurements were:

| Style | Body width | Available content width | Body position |
| --- | ---: | ---: | --- |
| Glass | 74.5 dp | 368 dp | Not below the header |
| Brief | 89.5 dp | 400 dp | Not below the header |
| Board | 96 dp | 372 dp | Not below the header |

At the after source head, `InboxReportedLargeTextLayoutTest` requires the expanded 200% body to start **below the header**, start at the content's left edge, reach its right edge, and occupy the **full available content width within 1 dp**. Separate 100% tests require the existing inline layout to remain unchanged.

## Rendering limits and visual review

These are JVM Robolectric native-graphics component captures, **not physical-device or emulator screenshots**. No physical-device or emulator run is claimed. They do not establish OEM/device text rendering, system-bar behavior, TalkBack behavior, touch accessibility, platform elevation, or Glass refraction. The expanded fixture is deliberately tall enough to contain the complete original, so these images are not evidence of scrolling behavior in a phone-sized viewport.

This matrix is provided **for visual review**. This README makes no visual-quality verdict; see the separate **independent visual review by `reported-large-text-review`** for the visual assessment. The path, hash, and geometry records above are objective checks and do not replace that review.
