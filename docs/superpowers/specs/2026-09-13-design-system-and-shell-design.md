# Design System and Application Shell — Design

**Status:** approved 2026-09-13 **Amends:** `CLAUDE.md` (the stack table's UI row changes primitive layer and drops Tremor), `docs/planning/03-frontend.md` (the section "Primitive layer: Radix, not Base UI" is replaced, because the condition it named for revisiting the decision is now met).

The fourteenth implementation spec. Every authenticated page in `apps/web` works and none of them is designed.

The frontend was built feature-first: links, domains, passwords, QR codes, each with correct behaviour, accessible markup, i18n from the first string, and tests. What it never received is a visual layer. `src/components/ui/` holds exactly one component — `button.tsx`. Navigation is a bare `<ul>` in a flex header. The link list is a `<ul>`. Six production files hand-roll 17 `<input>`, 16 `<label>` and 4 `<select>` elements with their own label/description/error wiring. The colour tokens are stock shadcn neutral greys, including all five `--chart-*` values.

This spec closes that gap and stops there. It adds no API surface, no new page, and no new capability. Its output is a design system, a shell to hang pages on, and every existing page moved onto both.

## Goal

`apps/web` looks and behaves like one designed product, built from a component set that can be extended by running `shadcn add` rather than by hand.

## Scope

### In scope

- Initialising shadcn/ui from a chosen preset, replacing `components.json`, `src/styles/app.css` and `ui/button.tsx`.
- Restructuring the colour tokens so a second colour theme is a CSS block rather than a refactor.
- Self-hosting the preset's two font families.
- Installing the component set the existing pages need, and no more.
- A sidebar-based application shell for the authenticated area; the public pages keep a header and footer.
- Moving every existing page onto that system. Markup changes; logic does not.
- Storybook stories and updated unit tests for everything touched.

### Out of scope, and why

- **The analytics page and the `chart` component.** It has its own spec and its own unsolved question (see "The chart ramp is monochromatic"). Installing `chart` here would ship a dependency nothing renders.
- **Members, folders, tags, audit log, team settings.** Each is an API surface with no frontend at all. Giving them pages is feature work wearing a design costume, and each deserves its own scope decision.
- **TanStack Table.** `docs/planning/03-frontend.md` promises the link list search, filtering and sorting. This spec gives the list `Table` _markup_. Behaviour is a separate change with its own tests, and bundling it here would make the diff impossible to review as a design change.
- **Toasts.** Feedback today is inline — `CopyButton` swaps its own label, mutations render messages in place. Introducing a toast layer changes flows and the assertions that cover them. That is behaviour, not appearance.
- **The user settings page and the theme picker.** Deferred deliberately; see "One axis now, two later".
- **A brand layer** — logo, favicon, wordmark. `apps/web/public/` does not exist and there is no mark to render. Brand identity is a separate exercise, possibly through Claude Design, and it sits _on top_ of the token foundation this spec lays rather than inside it.
- **Hand-editing generated `ui/*` components.** See "Generated components are not edited".

## Global constraints

Inherited and not re-litigated here:

- Accessibility is a requirement, not a nice-to-have: WCAG 2.1 AA, checked at two levels (Storybook's a11y addon per component, `@axe-core/playwright` per page).
- No hardcoded user-facing string. Every new or moved string has a catalogue key in both `en.json` and `de.json`.
- Dark and light mode are both first-class, from the first component.
- Conventional Commits, subject capped at 50 characters including type and scope; `pnpm format` before every commit; the Lefthook hooks are not bypassed.
- Never a `Co-Authored-By` line or a generator footer, in a commit or a PR body.
- `apps/web/src/routeTree.gen.ts` is only included in a commit when the change actually requires it.

## The primitive layer changes to Base UI, and Tremor is dropped

`CLAUDE.md` pins shadcn/ui on Radix, and `docs/planning/03-frontend.md` reasons it out at length. The entire argument rests on one fact: Tremor is built on Radix, so choosing Base UI would mean shipping two overlapping primitive libraries in one bundle. That document also names its own exit condition:

> Worth revisiting if Tremor ever migrates to Base UI itself, or if the analytics dashboard is ever rebuilt on a different charting library that doesn't carry its own primitive dependency (e.g. bare Recharts with a hand-built, Base UI-based dashboard shell) — that would remove the constraint and reopen Base UI as the natural default choice.

The condition is met, and the cost of acting on it has never been lower:

- **Tremor is not installed.** It appears in no `package.json` in this repository.
- **Radix's total footprint is one import** — `Slot` in `src/components/ui/button.tsx`.
- **shadcn's own `chart` component is Recharts v3** and ships in Base UI, React Aria and Radix variants. It carries no primitive dependency of its own.

So the analytics dashboard will be built on shadcn's `chart`, which is part of the same design system as everything else, rather than on a second vendor with its own theming. What is genuinely given up is Tremor's pre-built dashboard blocks: KPI tiles, time series and breakdown lists become ordinary composition work. Against one analytics page, that is a smaller cost than maintaining two primitive layers and two theming models forever.

## The preset

Chosen visually through `ui.shadcn.com/create` and recorded here as a single value:

```
pnpm dlx shadcn@latest init --preset b39ODpImW
```

| Setting      | Value           |
| ------------ | --------------- |
| Style        | `base-sera`     |
| Base Color   | Neutral         |
| Theme        | Indigo          |
| Chart Color  | Indigo          |
| Heading font | Geist           |
| Body font    | Geist           |
| Icon library | Lucide          |
| Radius       | None            |
| Menu         | Default / Solid |
| Menu Accent  | Subtle          |

The preset code is the authoritative record. The values below were measured from the live preview rather than assumed, and exist so a later reader can tell whether the tokens still match what was chosen:

- `--radius: 0`. Buttons, cards and inputs all compute a `border-radius` of `0px`. There is no radius override to apply and none should be added — the style's shapes are square by design.
- `--font-sans: "Geist", "Geist Fallback", 'Geist Variable', sans-serif`
- `--font-mono: "Geist Mono", "Geist Mono Fallback"`
- `--primary: oklch(0.457 0.24 277.023)`
- `--chart-1` … `--chart-5`: `oklch(0.785 0.115 274.713)`, `oklch(0.585 0.233 277.117)`, `oklch(0.511 0.262 276.966)`, `oklch(0.457 0.24 277.023)`, `oklch(0.398 0.195 277.366)`

`init` rewrites `components.json`, `src/styles/app.css` and `src/components/ui/button.tsx`. Three things in the current `app.css` are not shadcn's and must survive: `@custom-variant dark (&:where(.dark, .dark *))`, which is what makes `themeClassName()`'s `dark` class work; the `html, body, #app { min-height: 100% }` rule; and `body { margin: 0 }`.

### What the Sera style brings, accepted knowingly

Two of its characteristics are baked into the component classes, not into tokens, and therefore cannot be configured away:

**Headings and field labels render in capitals** — measured as `text-transform: uppercase` with `letter-spacing: 0.9px`. Two consequences were weighed. German interface copy runs longer than English and its compounds cost more width in capitals; `domains.recordsHeading` and the password-policy messages are already long. And capitalised text is harder to read for people with dyslexia. No WCAG 2.1 AA success criterion forbids capitals, so the accessibility commitment is not broken — but this is a real cost against an audience of occasional volunteer users, and it was accepted with that understood rather than overlooked.

**Input fields carry an underline, not a box.** The field boundary is weaker, which puts more weight on the focus indicator. That indicator must meet WCAG 1.4.11 (3:1 non-text contrast) in both light and dark mode, and this spec requires that to be verified rather than assumed.

Both belong to the style. The only ways to avoid them are choosing a different style or editing generated components, and the second is ruled out below.

### The chart ramp is monochromatic

`--chart-1` through `--chart-5` are five lightnesses of one indigo hue. Their lightness values run 0.785, 0.585, 0.511, 0.457, 0.398 — the darkest three are close together.

For a sequential series this is correct and even preferable. The analytics endpoint, however, serves **every dimension's top ten**, and `apps/api/internal/analytics/dimensions.go` writes eight of them: `browser`, `os`, `device`, `country`, `referrer`, `bot_status`, `qr_vs_regular`, and `utm_source` when present. Those are categorical, and five shades of one hue separate less well than five hues — a difficulty that compounds for colour-blind readers and is precisely the case where colour must not be the only carrier of meaning (WCAG 1.4.1).

Nothing in this spec renders a chart, so nothing here is blocked. The problem is recorded so the analytics spec inherits it as a stated open question rather than rediscovering it. Direct labelling instead of a colour legend is the obvious first answer; a separate categorical palette alongside the sequential ramp is the second.

**Answered 2026-09-18.** The question above was whether five lightnesses of one hue separate well enough to carry eight categorical dimensions; the answer is that they do not, and measuring the ramp to answer it found a second, more urgent defect underneath: the ramp was also failing WCAG 1.4.11's 3:1 floor outright, in both themes, for the same root cause as the `--ring` defect this spec fixed above — Theme=Indigo had left `--chart-1` through `--chart-5` at the base preset's values in both blocks. Neither of the two answers proposed above is what shipped: the series chart instead carries the metric on colour and the population on stroke style, two channels doing what one hue cannot. Full reasoning and the measured before/after numbers: `docs/superpowers/specs/2026-09-18-link-analytics-page-design.md` and `docs/superpowers/plans/2026-09-18-analytics-contrast.md`.

## One axis now, two later

Dark and light already travel as a `dark` class on `<html>`, written from a cookie the server reads so the first paint is correct (`readTheme`, `themeClassName`, `src/lib/preferences.ts`). A user settings page offering a choice of colour theme is planned, which makes colour a **second, independent axis**: colour theme × light/dark.

The token structure is therefore built for two axes now:

```css
[data-theme='indigo'] {
	/* every colour token */
}
[data-theme='indigo'].dark {
	/* its dark pairing */
}
```

`:root` keeps only what is not a colour — radius, fonts, spacing. `<html>` carries `data-theme="indigo"` alongside the existing `dark` class, emitted from `__root.tsx` the same way `lang` and the theme class already are.

**The attribute's value is hardcoded to `indigo` for now.** No cookie, no `COLOR_THEMES` union, no reader function, no picker. This is a deliberate line, and it splits the work where the cost actually is: restructuring every colour token is the expensive half and happens now, so a second theme later is a CSS block and one attribute value. Reading a cookie is the cheap half — roughly twenty lines following the pattern `readTheme` already establishes — and it buys nothing until something can write that cookie. Building the machinery now would mean a single-member union, a reader with one possible outcome, and tests asserting that the only value is the only value.

## Fonts are self-hosted

The preset names Geist for both heading and body, and defines `--font-mono` as Geist Mono — so two families are needed. The mono family is shipped because the token exists: any component reaching for `font-mono` would otherwise fall back to whatever the browser calls monospace, which is a different typeface on every machine.

They are loaded from `@fontsource-variable/geist` and `@fontsource-variable/geist-mono`, not from a font CDN. The audience is German associations and their visitors: fetching a font from Google's servers transmits the visitor's IP address to Google, which is the practice a Munich court ruled against in 2022, and `docs/planning/08-legal-and-compliance.md` already owes those visitors a privacy policy that does not have to explain it. Self-hosting removes the question instead of answering it.

Both packages are at 5.3.0, published 2026-07-19 — 56 days old at the time of writing, which clears the workspace's `minimumReleaseAge` gate with room to spare.

## The component set

Installed because a page that exists today needs it:

`button` · `input` · `label` · `field` · `select` · `textarea` · `checkbox` · `card` · `table` · `badge` · `alert-dialog` · `dropdown-menu` · `sidebar` · `empty` · `input-group` · `pagination` · `separator` · `skeleton` · `spinner`

`sidebar` pulls `sheet`, `tooltip`, `separator` and `skeleton` along with it; they are listed anyway so the set is readable as a whole.

Not installed: `chart` (arrives with analytics), `data-table` (arrives with list filtering), `calendar` and `date-picker` (the expiry field stays a native `<input type="datetime-local">`), and everything else in the registry.

`field` is the load-bearing one. shadcn's own TanStack Form integration composes `Field`, `FieldLabel`, `FieldDescription`, `FieldError` and `FieldGroup` around `form.Field` — which is exactly the shape the six form-bearing files reimplement by hand today, each with its own `aria-describedby`/`aria-invalid`/error-id wiring. Moving them onto `Field` replaces six private conventions with one, and the accessibility attributes stop being something each file has to remember.

### Generated components are not edited

Files under `src/components/ui/` are `shadcn add` output and stay byte-identical to it, import style included. They will use `@/…` paths while the rest of the codebase uses relative imports; that inconsistency is the price of re-runnable updates and is accepted. `vite.config.ts:73` sets `resolve: { tsconfigPaths: true }` and `tsconfig.json` maps both `@/*` and `#/*` to `./src/*`, so those imports resolve without further configuration.

The rule exists because the alternative was considered and rejected. Reaching the desired corner radius by rewriting rounding classes inside generated components would have worked exactly once: the next `shadcn add` or component update silently restores the original, and nothing fails loudly when it does. Customisation belongs in tokens, in wrapper components, or in the choice of preset.

## The shell

**`AppSidebar`** is new. Its header holds the team switcher, rebuilt on `DropdownMenu`; its content holds the section navigation with icons; its footer holds language, theme and sign-out. `authed-shell.tsx` becomes `SidebarProvider` + `AppSidebar` + `SidebarInset`, with a slim top bar carrying the `SidebarTrigger` and the current page's title. Collapsing, the mobile sheet and keyboard handling come with the component.

A sidebar rather than a header because the section list is going to grow. Today it is Links and Domains; folders, tags, members, statistics, the audit log and team settings are all API surfaces waiting for pages. In a sidebar each of those is one more row. In a header they are a crowding problem, and solving it later would mean moving every page a second time.

The public pages — the landing page, login, and the not-found route — keep `SiteHeader` and `SiteFooter`. They have no team context and nothing to navigate between; a sidebar there would be chrome around a single sentence.

`AuthedShell` is presentational today, taking plain props and a callback so it can be rendered without a router, a `QueryClient` or a session. That property is preserved: the sidebar takes the same props and the route component keeps the wiring.

## Moving the pages

Logic is untouched throughout. Loaders, mutations, error classification, cache invalidation and validation all stay exactly as they are.

- **Link list** — `<ul>` becomes `Table`; the password indicator becomes a `Badge`; the empty state becomes `Empty`; paging becomes `Pagination`.
- **Link detail** — the password and QR sections become `Card`s.
- **Domains** — the existing `<table>` becomes `Table`. `pending` / `verified` / `failed` become a `Badge` carrying **icon and text as well as colour**; colour never carries the status alone (WCAG 1.4.1). The DNS record instructions keep their copy buttons.
- **Forms** — `link-form.tsx`, `link-password-card.tsx`, `link-qr-card.tsx`, `new-team.tsx`, the domain claim form in `teams.$teamSlug.domains.tsx` and `login.tsx` all move to `Field` + `Input`/`Select` + `FieldError`.
- **QR colour fields** — `InputGroup` with a leading `#`, replacing the hand-built hex entry.
- **Delete confirmations** — `confirm-delete.tsx` becomes `AlertDialog`.
- **Team switcher** — `DropdownMenu`, relocated into the sidebar header.
- **Language switcher and theme toggle** — relocated into the sidebar footer.

## Hazards for whoever implements this

Four things that will otherwise be discovered the hard way.

**`cn` is not shadcn's `cn`.** `src/lib/utils.ts` re-exports from the npm package `cn` (0.2.6), which describes itself as a drop-in replacement for clsx + tailwind-merge with no dependencies. Generated components rely on that function _resolving_ conflicting Tailwind classes, not merely concatenating them — a component whose `size-8` cannot be overridden by a passed `size-10` fails quietly and only in the cases where someone overrides. The claim is the package's own, so it is verified once with a test asserting that a later conflicting class wins, before anything is built on top. `init` will offer to overwrite `lib/utils.ts`; the existing re-export is kept.

**oxlint's `react/forbid-component-props` fires on every generated component.** `ui/button.tsx` carries a reasoned inline exemption for exactly this today. Across twenty components that becomes twenty comments nobody will keep accurate — so it becomes one override scoped to `components/ui/**` in `oxlint.config.ts` instead, with the reasoning recorded at the override rather than repeated at each site.

**Existing tests will break where roles change.** `<ul>` becoming `Table` changes what `getByRole` finds; `<input>` moving into `Field` changes how labels associate. Those assertions are **updated to the new roles, never loosened** to whatever still passes. A test that stops checking the accessible name is a test that stopped doing its job.

**`init` is destructive to three files.** `components.json`, `src/styles/app.css` and `ui/button.tsx` are overwritten. The three non-shadcn rules in `app.css` named above have to be reinstated afterwards, and the dark variant in particular is silent when lost: the class is still written to `<html>`, the tokens simply stop responding to it.

## Testing

- **Every touched component gets a Storybook story in both themes.** The a11y addon runs per story, which is where component-level contrast and labelling problems surface. Light and dark are separate stories, not a toggle, so both are actually exercised in CI.
- **Focus indicators are measured, not assumed.** Underlined inputs make the focus ring the primary affordance; its contrast against the background is checked against WCAG 1.4.11's 3:1 in both modes, and the measured values are recorded in the implementation plan.
- **The Indigo palette's text contrast is checked once by hand** in both modes — foreground on background, primary-foreground on primary, muted-foreground on background — and the numbers written down. Automated per-story checks catch regressions afterwards; they do not replace knowing the baseline.
- **The `cn` merge behaviour gets one unit test** asserting that a later conflicting utility class wins over an earlier one. It is three lines and it removes an assumption the whole component set rests on.
- **`@axe-core/playwright` continues to cover whole pages**, which is where composition-level problems live: focus order across the sidebar and content, and the landmark structure the new shell introduces.
- **Existing unit tests are updated to the new roles and kept as strict as they were.**
- The full gate stays green: `pnpm lint`, `pnpm format:check`, `pnpm typecheck`, `pnpm test`, `pnpm test:storybook`, `pnpm --filter web build`.

## Documentation this changes

- `CLAUDE.md`: the stack table's UI row becomes shadcn/ui on **Base UI** with shadcn `chart` for analytics, and the parenthetical defending Radix is removed. The `apps/web` description gains the design system's two load-bearing rules — generated `ui/*` is never hand-edited, and colour tokens live under `[data-theme='indigo']` rather than `:root`.
- `docs/planning/03-frontend.md`: the section "Primitive layer: Radix, not Base UI" is replaced by one recording the reversal, the condition that triggered it, and the measurements that made the cost negligible — Tremor never installed, Radix reduced to a single import. The Tremor row in the "Decided" table changes to shadcn `chart` on Recharts.
- `docs/planning/03-frontend.md` also gains the font decision and its reason, since self-hosting is a privacy constraint rather than a styling preference.
