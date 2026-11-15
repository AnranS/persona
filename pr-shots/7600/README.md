# Android reported-detail captures

This index separates the 12 pre-existing default-text PNGs from the 12 supplementary 200% text PNGs. The captures are visual review artifacts, not a claim of device or accessibility-interaction testing.

## Original default-text captures

These 12 files were already present on this artifact branch. Their original source revision is not established by this index; no source commit is asserted for them.

Matrix: Board, Brief and Glass, each in light and dark, with collapsed and expanded states at default text size.

### Collapsed

- [reported-collapsed-board-dark.png](reported-collapsed-board-dark.png)
- [reported-collapsed-board-light.png](reported-collapsed-board-light.png)
- [reported-collapsed-brief-dark.png](reported-collapsed-brief-dark.png)
- [reported-collapsed-brief-light.png](reported-collapsed-brief-light.png)
- [reported-collapsed-glass-dark.png](reported-collapsed-glass-dark.png)
- [reported-collapsed-glass-light.png](reported-collapsed-glass-light.png)

### Expanded

- [reported-expanded-board-dark.png](reported-expanded-board-dark.png)
- [reported-expanded-board-light.png](reported-expanded-board-light.png)
- [reported-expanded-brief-dark.png](reported-expanded-brief-dark.png)
- [reported-expanded-brief-light.png](reported-expanded-brief-light.png)
- [reported-expanded-glass-dark.png](reported-expanded-glass-dark.png)
- [reported-expanded-glass-light.png](reported-expanded-glass-light.png)

## Supplementary 200% text captures

- Base main: `f06b447d372e66b3f2022512e155756a7d4e95cd`.
- Test-only capture head: `1e00ea11f7c65124e97d4c508d6a257801b26274`.
- Matrix: Board, Brief and Glass, each in light and dark, with collapsed and expanded states.
- State coverage: collapsed rows show the intentional preview; expanded rows show the complete reported content through `S1-END`.
- Text and density: `fontScale=2.0` (200% text), with the xhdpi density unchanged.
- Capture limits: native Robolectric graphics only; no emulator or physical device was used. TalkBack behavior was not tested, so these images are not evidence of screen-reader interaction.

From `android/`, after configuring the standard v9 Android environment, reproduce the focused captures with:

```bash
flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxSectionHeaderShotsTest' --tests 'persona.feature.inbox.InboxReportedShotsTest'
```

### Collapsed at 200% text

- [reported-collapsed-board-dark-200pct.png](reported-collapsed-board-dark-200pct.png)
- [reported-collapsed-board-light-200pct.png](reported-collapsed-board-light-200pct.png)
- [reported-collapsed-brief-dark-200pct.png](reported-collapsed-brief-dark-200pct.png)
- [reported-collapsed-brief-light-200pct.png](reported-collapsed-brief-light-200pct.png)
- [reported-collapsed-glass-dark-200pct.png](reported-collapsed-glass-dark-200pct.png)
- [reported-collapsed-glass-light-200pct.png](reported-collapsed-glass-light-200pct.png)

### Expanded at 200% text

- [reported-expanded-board-dark-200pct.png](reported-expanded-board-dark-200pct.png)
- [reported-expanded-board-light-200pct.png](reported-expanded-board-light-200pct.png)
- [reported-expanded-brief-dark-200pct.png](reported-expanded-brief-dark-200pct.png)
- [reported-expanded-brief-light-200pct.png](reported-expanded-brief-light-200pct.png)
- [reported-expanded-glass-dark-200pct.png](reported-expanded-glass-dark-200pct.png)
- [reported-expanded-glass-light-200pct.png](reported-expanded-glass-light-200pct.png)
