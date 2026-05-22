# Frontend Redesign - Flat Design Spec

Date: 2026-04-15

## Goal

Replace the current vanilla HTML frontend with a Vue 3 + TypeScript + Tailwind CSS SPA, applying a Flat Design aesthetic: zero shadows, bold color blocks, geometric clarity, scale-based hover feedback.

## Tech Stack

- **Vue 3** + **TypeScript** + **Vite** (build tool)
- **Tailwind CSS v4** via Vite plugin (local, no CDN)
- **Vue Router** — SPA routing (`/login`, `/`)
- **lucide-vue-next** — icons
- **Outfit** font — downloaded locally, no Google Fonts CDN
- No extra UI library

## Project Structure

```
internal/frontend/
├── src/
│   ├── App.vue
│   ├── main.ts
│   ├── router/
│   │   └── index.ts
│   ├── views/
│   │   ├── LoginView.vue
│   │   └── DashboardView.vue
│   ├── components/
│   │   ├── AppHeader.vue
│   │   ├── StatusCard.vue
│   │   ├── ProviderCard.vue
│   │   ├── ProviderModal.vue
│   │   ├── ModelMappingRow.vue
│   │   └── CertificateInfo.vue
│   ├── composables/
│   │   └── useApi.ts
│   └── styles/
│       └── main.css
├── index.html
├── package.json
├── tsconfig.json
├── vite.config.ts
└── dist/                  # Go embed.FS reads from here
```

## Design Tokens

### Colors

| Token | Value | Usage |
|-------|-------|-------|
| bg | `#FFFFFF` | Page background |
| fg | `#111827` | Primary text |
| primary | `#3B82F6` | Buttons, active tab, accent |
| primary-hover | `#2563EB` | Button hover |
| primary-light | `#EFF6FF` | Tag backgrounds |
| secondary | `#10B981` | Success, active status |
| secondary-light | `#ECFDF5` | Active provider card bg |
| accent | `#F59E0B` | Warnings, highlights |
| danger | `#EF4444` | Delete, error, logout |
| danger-light | `#FEF2F2` | Error hover bg |
| muted | `#F3F4F6` | Dashboard bg, input bg |
| border | `#E5E7EB` | Dividers, input focus |
| text-secondary | `#6B7280` | Labels, descriptions |

### Typography

- Font: `Outfit, sans-serif` (downloaded locally)
- Headings: 700-800 weight, `-0.02em` letter-spacing
- Body: 400 weight
- Labels/buttons: 600 weight, uppercase for section labels
- Monospace: `SF Mono, Monaco, 'Courier New', monospace` (for URLs/tokens)

### Shape & Radius

- Consistent `rounded-lg` (8px) for cards, buttons, inputs
- `rounded-full` for status dots, tags, badges
- No shadows anywhere. No gradients on UI elements.

### Interaction

- Hover: `scale(1.02)` for cards, `scale(1.05)` for buttons + color shift
- Focus: `ring-2 ring-blue-500`, no glow
- Transitions: `transition-all duration-200`
- Disabled state: `opacity-0.5`

## Page Designs

### Login Page (`/login`)

- White background with three decorative geometric shapes (circle, circle, rounded square) at low opacity (4%)
- Centered card, no border/shadow, just the form
- Blue logo icon (rounded square with layers SVG)
- Title: "Claude Code Proxy" (800 weight, 26px)
- Subtitle: gray descriptive text
- Input: gray bg, transparent border, blue border on focus
- Button: solid blue, white text, scale hover

### Dashboard Page (`/`)

**Header:**
- White background, 2px bottom border
- Left: blue logo icon + "Claude Code Proxy" title
- Right: "Logout" outline button (border turns red on hover)

**Tab Switcher:**
- White bg, 2px border, pill-style container
- Active tab: solid blue bg with white text
- Inactive: transparent, gray text

**Status Tab:**
- Three full-color cards in grid: Blue (status), Green (uptime), Amber (requests)
- White text on colored backgrounds
- Below: Active Provider info card (white bg, border, blue mapping tags)

**Providers Tab:**
- "Add Provider" button (blue, with + icon)
- Provider cards: white bg + 2px border
  - Active provider: green border + green-tinted bg + "Active" badge
  - Disabled provider: opacity 50%
  - Hover: slight scale + border turns blue
  - Actions: ghost/blue/green/amber/red small buttons

**Certificates Tab:**
- White cards with 2px border
- Labels in uppercase, small, gray
- Code block: dark bg (#111827) with blue copy button

## Go Backend Changes

Minimal change to `internal/admin/server.go` authMiddleware:
- For non-API routes without valid session, serve `index.html` instead of redirecting to `/login.html`
- Vue Router handles `/login` route client-side
- Keep `/api/*` auth logic unchanged

## Build Pipeline

1. `cd internal/frontend && npm install` (dev only)
2. `npm run build` → outputs to `internal/frontend/dist/`
3. Vite generates: `dist/index.html` + `dist/assets/*.js` + `dist/assets/*.css`
4. Go `embed.FS` embeds `dist/` folder, zero change to embedding logic

## Out of Scope

- Dark mode (can be added later)
- i18n (keep Chinese-only for now)
- New API endpoints
- Authentication logic changes
