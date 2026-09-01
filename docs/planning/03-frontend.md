# URL Shortener — Frontend Planning

Status: draft, reflecting decisions made through 2026-09-01. Intended to be refined further in Claude Code.

Project context: open-source URL shortener for non-profit associations ("Vereine"), one shared multi-tenant instance (see `01-architecture.md`). Frontend is TanStack Start (React), consuming the Go API.

## Decided

| Concern | Choice | Notes |
| --- | --- | --- |
| Routing / data fetching | TanStack Router + TanStack Query | Bundled with TanStack Start, not a separate choice |
| Forms | TanStack Form | Same ecosystem/publisher as Router and Query |
| Tables | TanStack Table | Powers the link list (search/filter by URL, alias, creator, time period; sort; pagination) |
| UI components | **shadcn/ui on Radix** (decided 2026-09-01) | See "Primitive layer: Radix, not Base UI" below — a deliberate exception to the general Base UI preference, made specifically for Tremor compatibility |
| Analytics dashboard | **Tremor** | Pre-built dashboard blocks (KPI tiles, time series, breakdowns) rather than building chart layout from scratch; open source, now maintained by Vercel (same vendor as hosting); built on Radix UI + Recharts |
| QR code generation | **Server-side, Go** (`piglig/go-qr`) | Matches every requirement in the original feature list (configurable size, all 4 ECC levels, margin, centered logo validated against ECC budget, custom colors, PNG + SVG). Frontend calls a preview endpoint rather than generating QR codes client-side — consistent with API-first design. |
| Icons | lucide-react | Standard pairing with shadcn/ui |
| Web auth UI | **Custom form using `supabase-js`** | Full control over look-and-feel, stays consistent with the shadcn/ui design system, rather than dropping in Supabase's pre-built Auth UI components. Separate from CLI auth (already decided as OAuth via Supabase's OAuth 2.1 Server, PKCE + loopback — see `01-architecture.md`) |
| Internationalization | **Built in from day one** | English as default, German as second language (both decided 2026-09-01) — see "Internationalization approach" below |
| Accessibility | **Explicit requirement, not incidental** (decided 2026-09-01) | Target WCAG 2.1 AA — see "Accessibility" below |
| Dark / light mode | **In MVP scope** (decided 2026-09-01) | Not deferred |
| Component documentation | **Storybook** (decided 2026-09-01) | See "Storybook" below |

## Primitive layer: Radix, not Base UI

Question raised 2026-09-01: the general preference is shadcn/ui on Base UI rather than Radix — which fits better alongside Tremor?

Finding: as of shadcn/ui's July 2026 changelog, **Base UI is now shadcn/ui's default** primitive layer (the community itself now picks Base UI over Radix roughly 2-to-1 on `shadcn/create`, and the shadcn team uses it in their own new projects). Radix is explicitly **not deprecated** — "every update and new component will ship for both libraries" — and remains available via the `-b radix` flag. Both are considered accessibility-first; Base UI's pitch is more active maintenance (built by the same people who built Radix, "with everything they learned") versus Radix's comparatively slower cadence.

**Tremor, however, is still built on Radix UI** (confirmed against Tremor's own site: "Built on Recharts and Radix UI"), not Base UI. There's no Base UI variant of Tremor.

**Decision: use shadcn/ui with Radix (`-b radix`) for this project**, not the new Base UI default — specifically because Tremor is already committed for the analytics dashboard. Running both Radix and Base UI side by side would work (they don't conflict), but means shipping two overlapping primitive/behavior libraries in the same bundle, and some risk of subtly inconsistent interaction details (focus trapping, animation timing) between Tremor's dialogs/popovers and the rest of the app's. Standardizing on Radix everywhere avoids that for zero accessibility cost — shadcn's own changelog confirms Radix isn't a second-class option, just no longer the default.

Worth revisiting if Tremor ever migrates to Base UI itself, or if the analytics dashboard is ever rebuilt on a different charting library that doesn't carry its own primitive dependency (e.g. bare Recharts with a hand-built, Base UI-based dashboard shell) — that would remove the constraint and reopen Base UI as the natural default choice.

## Accessibility

Decided 2026-09-01: accessibility is an explicit requirement, not a side effect of the component choice.

Target: **WCAG 2.1 AA.** This also happens to line up with Germany's Barrierefreiheitsstärkungsgesetz (BFSG, in force since June 2025), which references EN 301 549 / WCAG 2.1 AA for digital services offered to consumers — worth keeping in mind given the target audience is German Vereine, even though the exact applicability of BFSG to a non-profit community tool hasn't been separately reviewed (flagging this the same way the Safe Browsing licensing question was flagged earlier — a reasonable-fit assumption, not a legal confirmation).

Radix (the primitive layer, per the decision above) gives keyboard navigation, ARIA attributes, and focus management out of the box for interactive components — a solid foundation, but not a substitute for testing: color contrast in the final theme, form error announcements, and focus order across full pages still need explicit verification rather than being assumed from the component library alone.

## Internationalization approach

Decided 2026-09-01: build with i18n from day one; **English as the default language, German as the second**, both shipped at launch (this reverses the earlier placeholder assumption of German-only).

Recommended library: **react-i18next**, using the SSR-safe integration pattern documented for TanStack Start (loading translations server-side during SSR, hydrating consistently on the client to avoid flash-of-untranslated-content or hydration mismatches). Rationale over the alternative (Paraglide JS, a newer compile-time/type-safe approach also documented for TanStack Start): i18next's plain JSON translation files are far more approachable for non-developer contributors submitting translations via PRs, which matters more for an open-source community project than the stronger compile-time type safety Paraglide offers. If translation contributions turn out not to be a priority, Paraglide is worth reconsidering for its DX and bundle-size advantages.

Practical implication for implementation: every user-facing string needs a translation key from the first component built, not retrofitted later — this is a structural decision, not just a library pick, so it's worth setting up the pattern before the first real page is built rather than after. With two languages shipping at launch (not one placeholder language), neither can be hardcoded anywhere even temporarily.

## Storybook

Decided 2026-09-01: yes, set up Storybook for the component library.

This pairs naturally with the shadcn/ui "owned code" model — since components live directly in the repo rather than behind an npm package boundary, Storybook becomes the living reference for what's actually available and how each variant/state looks, which matters more (not less) once Tremor, shadcn/ui, and custom components are all coexisting in the same design system. Also a natural place to visually spot-check dark/light mode and the English/German string lengths against each other (translated strings are rarely the same length, which is a common place for UI to break).

## Design system notes

- Both shadcn/ui and Tremor are Tailwind-based, so the two compose without a styling mismatch.
- Dark/light mode is in MVP scope (decided above) — build it in from the first components rather than retrofitting a theme switch later.

## Translation ownership

Decided 2026-09-01: the app creator (developer/maintainer) owns translations for both English and German, rather than routing this through separate community translators for v1. Keeps quality and voice consistent while the project is small; revisit if/when other Vereine or contributors want to add further languages (see "Not yet decided" below).

## Testing strategy

Decided 2026-09-01: testing spans the full range from unit to E2E, not just one layer.

- **Unit tests**: **Vitest** + **React Testing Library** — this is TanStack Router/Start's own officially recommended setup (`vitest`, `@testing-library/react`, `@testing-library/jest-dom`, `@testing-library/user-event`, `jsdom`), including TanStack's router-specific test utilities (`renderWithRouter()`, `createTestRouter()`) for components that depend on route context. Covers individual components, hooks, and utility functions.
- **Integration tests**: same Vitest + RTL stack, but exercising multiple components/hooks together, with **MSW** (Mock Service Worker) mocking the Go API — lets tests run against realistic API responses without a live backend, including error states and edge cases that are awkward to reproduce against a real API.
- **E2E tests**: **Playwright**, driving real user flows end-to-end (create a link, view analytics, generate a QR code, add a custom domain, log in) against a running instance. This is the layer that actually exercises frontend + API + database together, so it's also where integration bugs between the three surface.
- **Accessibility testing, integrated at two levels** (resolves the earlier open question — yes to both, not one or the other): Storybook's a11y addon for fast, component-level, dev-time feedback (catches issues at the moment a component is built), and `@axe-core/playwright` inside the E2E suite for full-page, real-interaction-context checks (catches composition-level issues, like focus order across a whole page, that no single component's story would reveal). Both feed into the WCAG 2.1 AA target set earlier.

**Resolved 2026-09-01, see `07-repo-structure-and-tooling.md`**: unit/integration run on every PR via path-filtered GitHub Actions; E2E runs against the PR's actual Vercel preview deployment (not on every push) plus a nightly run against `main`.

Note: E2E tests inherently exercise the Go backend and Supabase too, but Go-side unit testing conventions belong in a future backend-focused planning doc, not here.

## Not yet decided / to revisit

- Whether to open translation contributions to the community later (and how review would work) once the project has external contributors.
