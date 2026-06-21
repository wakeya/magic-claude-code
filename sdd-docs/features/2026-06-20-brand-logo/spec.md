# Brand Logo Replacement Spec

Local page: login page, admin header, browser tab (favicon)
Proxy entry: none
Reference sources: source PNG (`/mnt/hgfs/VMShare/design-logo/bfbe6b50-322d-4838-861e-6a26c10c092e.png`, 1254×1254, 8-bit RGB)
Stack: Vue 3 + Vite + Tailwind, Go `embed.FS`
Last updated: 2026-06-20
Progress: 3 / 3 validated

## Overall Analysis (Source Analysis)

### Current Branding State

The project currently uses an inline SVG geometric icon (stacked chevrons) as the logo in two locations:

1. **Login page** (`internal/frontend/src/views/LoginView.vue:8-12`): `app-logo-mark` container at `w-14 h-14` (56×56 px) with a 28×28 inline SVG inside. The user reports this is too small.
2. **Admin header** (`internal/frontend/src/components/AppHeader.vue:4-8`): same SVG, container `w-8 h-8` (32×32 px) with 18×18 SVG inside.
3. **Favicon**: none. `internal/frontend/dist/index.html` has no `<link rel="icon">` tag; no `favicon.ico` file exists.

The `app-logo-mark` class applies a solid accent-color background (`var(--app-accent)`) behind the SVG. With a PNG bitmap logo that has its own visual identity, this solid background frame becomes a design decision rather than a default.

### Source Asset

| Property | Value |
| --- | --- |
| Path | `/mnt/hgfs/VMShare/design-logo/bfbe6b50-322d-4838-861e-6a26c10c092e.png` |
| Dimensions | 1254 × 1254 px |
| Aspect ratio | 1:1 (square) |
| Format | PNG, 8-bit RGB, non-interlaced |
| Size | 956 KB |

Because the source is square and high-resolution, all target sizes (favicon 16/32/48, header 32, login 96) can be produced via proportional downscaling with no cropping.

### Static Asset Pipeline

- Vite builds into `internal/frontend/dist/`; Go embeds this directory via `embed.FS` and serves it through `http.FileServer`.
- Vite's default `public/` directory is not currently used. Assets placed in `internal/frontend/public/` are copied verbatim to `dist/` at build time, which is the correct location for `favicon.ico` and PNG logos that must be referenced by stable URLs.
- Vue components reference assets either by URL (for `public/` files) or by import (for bundled assets). For logos that must not be hashed, `public/` is the right choice.

### Tooling for Image Processing

Two candidate approaches for producing `favicon.ico` (multi-size) and scaled PNGs:

1. **ImageMagick** (`convert`/`magick`): ubiquitous on Linux, can produce multi-resolution `.ico` directly from PNG.
2. **Go image libraries** (e.g., `github.com/disintegration/imaging` + a `.ico` encoder): adds Go module dependencies.

Recommendation: use ImageMagick at build time for one-shot asset generation; commit the generated assets to `internal/frontend/public/`. No runtime image processing is needed.

### Risk Summary

1. The source PNG is 8-bit RGB (no alpha channel). If the logo has white/background pixels, they will be visible against dark UI themes. Decision required: keep as-is, or pre-process to add transparency.
2. Removing the `app-logo-mark` accent-color background changes visual hierarchy on both login and header. Decision required on whether to keep it.
3. `favicon.ico` must be served from the site root path (`/favicon.ico`). Vite's `public/` directory handles this automatically.
4. The committed PNG assets will be embedded into the Go binary via `embed.FS`, increasing binary size. Source PNG is 956 KB; downscaled variants will be much smaller (estimated < 50 KB total).

## Development Checklist

| # | Status | Task | Output | Verification |
| --- | --- | --- | --- | --- |
| 1 | Planned | Generate image assets (favicon.ico + scaled PNGs) | `internal/frontend/public/favicon.ico`, `public/logo-header.png`, `public/logo-login.png` | `file favicon.ico` shows multi-resolution ICO; PNG dimensions match spec |
| 2 | Planned | Integrate logos into frontend | Modified `LoginView.vue`, `AppHeader.vue`, `index.html`, `main.css` | Frontend build passes; logos render at intended sizes in both light and dark themes |
| 3 | Planned | Build and visual verification | Build artifacts, screenshot evidence | `npm run build` succeeds; manual browser check of login + header + browser tab |

## Requirements

### Deliverables

1. `internal/frontend/public/favicon.ico` — multi-resolution ICO containing 16×16, 32×32, and 48×48 images, produced by proportional downscaling of the source PNG.
2. `internal/frontend/public/logo-header.png` — PNG at 64×64 px (2× of the 32 px display size for retina), used in the admin header.
3. `internal/frontend/public/logo-login.png` — PNG at 192×192 px (2× of the 96 px display size for retina), used on the login page.
4. `internal/frontend/dist/index.html` (via Vite `index.html` template) includes `<link rel="icon" href="/favicon.ico" />` in the `<head>`.
5. `LoginView.vue` replaces the inline SVG with an `<img>` referencing `/logo-login.png`, displayed at `w-24 h-24` (96×96 px) — an upsize from the current 56 px to address the "too small" feedback.
6. `AppHeader.vue` replaces the inline SVG with an `<img>` referencing `/logo-header.png`, displayed at `w-8 h-8` (32×32 px) — matching the current size for visual continuity in the header.
7. The `app-logo-mark` accent-color background is removed from both logo locations; the PNG stands on its own. (Decision point — see below.)
8. Generated assets are committed to `internal/frontend/public/`; after `npm run build` they are copied to `dist/` and embedded into the Go binary.
9. A small note is added to `CLAUDE.md` "Common Questions" or project README explaining where logo source assets live and how to regenerate them.

### Directory Structure

```text
internal/frontend/
  public/                 ← new directory (Vite static assets)
    favicon.ico           ← multi-resolution (16, 32, 48)
    logo-header.png       ← 64×64
    logo-login.png        ← 192×192
  src/
    views/
      LoginView.vue       (modified: replace SVG with <img>, remove app-logo-mark)
    components/
      AppHeader.vue       (modified: replace SVG with <img>, remove app-logo-mark)
    styles/
      main.css            (modified: drop or repurpose .app-logo-mark)
  index.html              (source template, if present — or dist/index.html via Vite)
```

### Constraints

1. Source PNG is 8-bit RGB with no alpha channel. If transparency is needed (e.g., for dark theme), a pre-processing step must flatten against transparent background. **Decision required** (see Decision Points below).
2. All downscaling must preserve the 1:1 aspect ratio — no stretching.
3. `favicon.ico` must include at minimum 16×16 and 32×32 sizes for cross-browser support; 48×48 is recommended for Windows desktop shortcuts.
4. PNG assets for display must be at 2× the CSS display size to support high-DPI screens.
5. The logo must remain legible in both light and dark themes. If the source PNG's background pixels cause contrast issues in dark mode, transparency processing becomes mandatory.
6. No runtime image processing in the Go binary — all assets are pre-generated at build time.
7. Asset filenames must be stable (no content hashes) so that `<img src="/logo-*.png">` references work without build-time URL resolution.

### Edge Cases

1. Browser requests `/favicon.ico` directly (not via HTML link) — Vite's `public/` handling serves it correctly at the root path.
2. User on a high-DPI display — 2× PNG assets ensure crisp rendering.
3. Dark theme active — logo must remain visible; depends on Decision D-002.
4. Source PNG has a white/light background that clashes with dark theme — transparency pre-processing required.
5. ImageMagick not available on developer machine — document the install command in the spec.
6. `internal/frontend/public/` directory did not exist before — Vite creates it automatically; no special handling needed.

### Non-Goals

1. SVG version of the logo — only raster PNG/ICO is in scope.
2. Dark-theme-specific variant of the logo — a single asset must work in both themes (via transparency if needed).
3. Logo animation or transitions.
4. Changes to the GitHub/GitLab/GitCode repository avatar — only the in-app logo is in scope.

## Task Details

### Task 1: Generate Image Assets

#### Requirements

**Objective** — Produce the three required image files from the source PNG via proportional downscaling.

**Outcomes** — `internal/frontend/public/favicon.ico` (16+32+48), `internal/frontend/public/logo-header.png` (64×64), `internal/frontend/public/logo-login.png` (192×192).

**Evidence** — `file`/`identify` output confirms dimensions and ICO multi-resolution structure; visual inspection shows no distortion.

**Constraints** — Source aspect ratio 1:1 must be preserved. ImageMagick is the recommended tool. If the source has a non-transparent background that clashes with dark UI, apply alpha-channel pre-processing first (see Decision D-002).

**Edge Cases** — ImageMagick not installed (document `apt-get install imagemagick` or equivalent); source PNG mode is RGB not RGBA (may need `-alpha set` before transparency work).

**Verification** — Run `identify internal/frontend/public/favicon.ico internal/frontend/public/logo-*.png` and confirm dimensions.

#### Plan

1. Create `internal/frontend/public/` directory.
2. Generate multi-resolution ICO: `convert source.png -define icon:auto-resize=48,32,16 public/favicon.ico`.
3. Generate header PNG: `convert source.png -resize 64x64 public/logo-header.png`.
4. Generate login PNG: `convert source.png -resize 192x192 public/logo-login.png`.
5. (Conditional on D-002) Apply transparency pre-processing before steps 2–4.

#### Verification

- [ ] `favicon.ico` contains 16, 32, and 48 px sizes.
- [ ] `logo-header.png` is 64×64.
- [ ] `logo-login.png` is 192×192.
- [ ] All assets have 1:1 aspect ratio.

### Task 2: Integrate Logos into Frontend

#### Requirements

**Objective** — Replace the inline SVG placeholders with `<img>` references to the generated PNG assets, add the favicon link, and adjust the login logo size.

**Outcomes** — `LoginView.vue` uses `<img src="/logo-login.png">` at `w-24 h-24`; `AppHeader.vue` uses `<img src="/logo-header.png">` at `w-8 h-8`; `index.html` includes favicon `<link>`; `app-logo-mark` background is removed or adjusted per Decision D-001.

**Evidence** — Frontend builds without errors; visual check in browser shows the new logo on login, header, and browser tab in both light and dark themes.

**Constraints** — Logo `<img>` must have `alt` attribute for accessibility. The login logo display size increases from 56 px to 96 px. The header logo stays at 32 px. The `app-logo-mark` CSS class may need to be removed or retained per D-001.

**Edge Cases** — Image fails to load (show `alt` text); dark theme reveals background clash (mitigated by D-002); layout shift on login page due to larger logo (check spacing).

**Verification** — `npm run build` succeeds; manual browser check in both themes.

#### Plan

1. Update `LoginView.vue`: replace the `app-logo-mark` div + inline SVG with `<img src="/logo-login.png" alt="Magic Claude Code" class="w-24 h-24 mx-auto mb-7" />`.
2. Update `AppHeader.vue`: replace the `app-logo-mark` div + inline SVG with `<img src="/logo-header.png" alt="Magic Claude Code" class="w-8 h-8" />`.
3. Add `<link rel="icon" href="/favicon.ico" />` to the Vite `index.html` template (create the template if only `dist/index.html` exists).
4. Remove or repurpose `.app-logo-mark` in `main.css` per Decision D-001.
5. Run `npm run build` to regenerate `dist/`.

#### Verification

- [ ] Login page shows the new logo at 96×96.
- [ ] Header shows the new logo at 32×32.
- [ ] Browser tab shows the favicon.
- [ ] Both themes render the logo with acceptable contrast.
- [ ] `npm run build` passes.

### Task 3: Build and Visual Verification

#### Requirements

**Objective** — Confirm the full build pipeline works and the logos render correctly in the running application.

**Outcomes** — A build artifact with embedded logos; screenshot evidence of login page, admin header, and browser tab.

**Evidence** — `npm run build` output; `go build` success; screenshots from a browser session.

**Constraints** — Must verify both light and dark themes. Must verify favicon appears in browser tab.

**Edge Cases** — Favicon cached by browser (hard refresh or incognito); logo not embedded because `public/` was not picked up by Vite (verify `dist/favicon.ico` exists after build).

**Verification** — Full build + manual browser check.

#### Plan

1. Run `npm --prefix internal/frontend run build`.
2. Verify `internal/frontend/dist/favicon.ico`, `logo-header.png`, `logo-login.png` exist.
3. Run `go build ./...`.
4. Start the server, open in browser, screenshot login + admin in both themes.
5. Confirm favicon in browser tab.

#### Verification

- [ ] `dist/` contains all three asset files.
- [ ] Go binary builds.
- [ ] Login page logo is visibly larger than before.
- [ ] Header logo renders correctly.
- [ ] Favicon visible in browser tab.
- [ ] Both themes look correct.

## Decisions (Approved 2026-06-20)

### D-001: `app-logo-mark` Background Treatment — Option (B)

Keep a rounded container with transparent background — provides consistent spacing/radius without color clash. The `.app-logo-mark` class is updated to remove `background: var(--app-accent)`; `border-radius` is retained.

### D-002: Transparency / Dark Theme Handling — Option (B)

Pre-process to add transparency via ImageMagick (`-fuzz` + `-transparent white`). Near-white pixels become transparent, allowing the logo to adapt to both light and dark themes.

### D-003: Login Logo Final Size — Option (B)

96 px (`w-24`) — substantial increase from the current 56 px without dominating the login card.
