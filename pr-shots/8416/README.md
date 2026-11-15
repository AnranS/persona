# Issue #8416: Inbox View handling screenshots

Public issue: `nwverse/persona#8416`.

Captured code HEAD: `f357b10ef2fafe41806fed9749e8bf7bd21f6415`.

## Capture conditions

- Non-embedded Inbox item `sc-1`, captured after clicking **View original**, with the resulting **View handling** action visible.
- English UI.
- Fixed time: `2026-01-01T12:00:00Z`; timezone: UTC.
- Wide CSS viewport: 1280 × 900; phone CSS viewport: 390 × 844. Both use device pixel ratio 2, producing 2560 × 1800 and 780 × 1688 PNGs respectively.
- Light and dark themes.
- Standard text and the product's Large text setting. Large is selected with `persona.text-size=large` and uses the 14/13 text scale; browser zoom is never used.

## Files

| File | Viewport | Theme | Text size |
| --- | --- | --- | --- |
| `sc1-en-wide-light-expanded.png` | Wide | Light | Standard |
| `sc1-en-wide-dark-expanded.png` | Wide | Dark | Standard |
| `sc1-en-phone-light-expanded.png` | Phone | Light | Standard |
| `sc1-en-phone-dark-expanded.png` | Phone | Dark | Standard |
| `sc1-en-wide-light-expanded-large.png` | Wide | Light | Large |
| `sc1-en-wide-dark-expanded-large.png` | Wide | Dark | Large |
| `sc1-en-phone-light-expanded-large.png` | Phone | Light | Large |
| `sc1-en-phone-dark-expanded-large.png` | Phone | Dark | Large |

## Verification

- All eight PNGs were individually opened and visually reviewed. Each shows the exact **View handling** label on one line, fully visible and untruncated, with no clipping or overlap.
- All 78 capture assertions passed (78/78).
- Capture used an isolated mock hub and Vite server with `PERSONA_HUB_URL` set explicitly. Server cleanup was confirmed after capture.
- Only the eight final expanded-state PNGs are included; there are no comparison images or local capture manifests.
