# Android large-text section headers

These are post-merge screenshot artifacts for follow-up visual review. They are not a claim that a person reviewed the images before the associated pull request merged.

## Provenance

- Source main commit: `aa5e1ec43e49a46609ada1da4906d4cf41e04db6`.
- The images are Robolectric native-graphics screenshots from the named existing Android screenshot tests; no emulator or physical device was used.
- Test: `InboxSectionHeaderShotsTest`.
- This test uses its own English-qualified synthetic records (fixture timestamp `2026-11-13`); it does not read `showcase.json` or inject the showcase clock/UTC.
- Reproduce from the repository root with `./android/gradlew -p android -PpersonaApplicationId=com.example.persona :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxSectionHeaderShotsTest'` (the other review classes use the same Gradle task).

## Matrix

12 section-header PNGs in Board, Brief and Glass, light and dark, at 1x and 2x text. The existing test is explicitly en-qualified.

## Files

- [board-dark-1.0x.png](board-dark-1.0x.png)
- [board-dark-2.0x.png](board-dark-2.0x.png)
- [board-light-1.0x.png](board-light-1.0x.png)
- [board-light-2.0x.png](board-light-2.0x.png)
- [brief-dark-1.0x.png](brief-dark-1.0x.png)
- [brief-dark-2.0x.png](brief-dark-2.0x.png)
- [brief-light-1.0x.png](brief-light-1.0x.png)
- [brief-light-2.0x.png](brief-light-2.0x.png)
- [glass-dark-1.0x.png](glass-dark-1.0x.png)
- [glass-dark-2.0x.png](glass-dark-2.0x.png)
- [glass-light-1.0x.png](glass-light-1.0x.png)
- [glass-light-2.0x.png](glass-light-2.0x.png)

## Supplementary open-menu captures at 200% text

The original 12 captures and their provenance above are unchanged. These six additional captures show the section-header menu genuinely open.

- Base main: `f06b447d372e66b3f2022512e155756a7d4e95cd`.
- Test-only capture head: `1e00ea11f7c65124e97d4c508d6a257801b26274`.
- Matrix: Board, Brief and Glass, each in light and dark, with the menu open in every image.
- Text and density: `fontScale=2.0` (200% text), with the xhdpi density unchanged.
- Capture limits: native Robolectric graphics only; no emulator or physical device was used, so device-specific rendering and interaction remain unverified.

From `android/`, after configuring the standard v9 Android environment, reproduce the focused captures with:

```bash
flock /tmp/inbox-matters-android-gradle.lock timeout 1800 ./gradlew :feature:inbox:testDebugUnitTest --tests 'persona.feature.inbox.InboxSectionHeaderShotsTest' --tests 'persona.feature.inbox.InboxReportedShotsTest'
```

### Supplementary files

- [board-dark-2.0x-menu-open.png](board-dark-2.0x-menu-open.png)
- [board-light-2.0x-menu-open.png](board-light-2.0x-menu-open.png)
- [brief-dark-2.0x-menu-open.png](brief-dark-2.0x-menu-open.png)
- [brief-light-2.0x-menu-open.png](brief-light-2.0x-menu-open.png)
- [glass-dark-2.0x-menu-open.png](glass-dark-2.0x-menu-open.png)
- [glass-light-2.0x-menu-open.png](glass-light-2.0x-menu-open.png)
