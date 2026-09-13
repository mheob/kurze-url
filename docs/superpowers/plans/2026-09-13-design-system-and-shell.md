# Design System and Application Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `apps/web` one designed visual system — shadcn/ui on Base UI, a sidebar shell, and every existing page moved onto both — without changing a single behaviour.

**Architecture:** shadcn/ui is initialised from a recorded preset, which owns the tokens and generates the components. Colour tokens are scoped to `[data-theme='indigo']` so a second colour theme is later a CSS block rather than a refactor. Files under `src/components/ui/` are generator output and are never hand-edited; everything bespoke composes them from outside.

**Tech Stack:** TanStack Start (React 19) · shadcn/ui on Base UI (`base-sera`) · Tailwind CSS 4 · `@tanstack/react-form` · Vitest + React Testing Library + MSW · Storybook 10 with the a11y addon and the Vitest addon (real Chromium) · Playwright + axe-core

**Spec:** `docs/superpowers/specs/2026-09-13-design-system-and-shell-design.md`

## Global Constraints

- **Preset:** `pnpm dlx shadcn@latest init --preset b39ODpImW -b base` — `base-sera`, Base Color Neutral, Theme Indigo, Chart Color Indigo, Geist / Geist, Lucide, Radius None, Menu Default / Solid, Menu Accent Subtle. **`-b base` is not optional and `--preset` does not carry it.** The CLI's `-b, --base <base>` selects the primitive library (`base`, `radix`, `aria`) independently of the preset, and its default is `radix`. Omitting it writes `"style": "radix-sera"` into `components.json` and keeps `radix-ui` — which looks right at a glance, because the Sera style, the Indigo palette and the fonts all land correctly either way. Corrected 2026-09-13 after exactly that happened on the first run.
- **Accessibility is WCAG 2.1 AA**, checked at two levels: Storybook's a11y addon per story (`a11y: { test: 'error' }` in `.storybook/preview.tsx` — it fails the run) and `@axe-core/playwright` per page.
- **No hardcoded user-facing string.** Every new or changed string gets a key in **both** `apps/web/src/i18n/locales/en.json` and `de.json`. `react/jsx-no-literals` is error-level and will catch a miss.
- **Files under `apps/web/src/components/ui/` are `shadcn add` output and are never hand-edited**, import style included. They use `@/…` paths; the rest of the codebase uses relative imports. That is accepted.
- **Behaviour does not change.** Loaders, mutations, `classifyApiError` handling, cache invalidation and validation stay exactly as they are. Only markup changes.
- **Every git write goes through GitButler.** Never `git add` / `git commit` / `git checkout`. The lane for this work is `feat/design-system-and-shell`, which already exists and carries the spec commit.
- **Conventional Commits, subject capped at 50 characters including type and scope.** Imperative mood, lowercase first letter, no trailing period.
- **Never a `Co-Authored-By` line or a generator footer**, in a commit or a PR body.
- **Run `pnpm format` before every commit** — `but commit` does not run the Lefthook hooks, so the formatter has to be invoked by hand. Never bypass hooks anywhere else.
- **`apps/web/src/routeTree.gen.ts` is only committed when the change actually requires it.** No task here adds or renames a route, so it should never appear in a diff.
- The full gate for every task: `pnpm lint`, `pnpm format:check`, `pnpm typecheck`, `pnpm test`, `pnpm test:storybook`.

---

## File Structure

**Created:**

- `apps/web/src/components/app-sidebar.tsx` — the authenticated navigation: team switcher, section links, account controls. Presentational and prop-driven, like `AuthedShell` is today.
- `apps/web/src/components/app-sidebar.stories.tsx`, `apps/web/src/components/app-sidebar.test.tsx`
- `apps/web/src/lib/utils.test.ts` — the `cn` merge guarantee.
- `apps/web/src/components/ui/*` — generator output (Tasks 2 and 4).

**Modified:**

- `apps/web/components.json`, `apps/web/src/styles/app.css` — rewritten by `init`, then repaired and restructured.
- `apps/web/src/routes/__root.tsx` — `<html>` gains `data-theme`.
- `apps/web/.storybook/preview.tsx` — the decorator gains `data-theme`, or every story loses its colours.
- `apps/web/src/components/authed-shell.tsx` — becomes the sidebar shell.
- `apps/web/src/components/link-list.tsx`, `domain-list.tsx`, `team-switcher.tsx`, `confirm-delete.tsx`, `link-form.tsx`, `link-password-card.tsx`, `link-qr-card.tsx`, `short-url-notice.tsx`, `site-header.tsx`, `site-footer.tsx`
- `apps/web/src/routes/login.tsx`, `_authed/new-team.tsx`, `_authed/teams.$teamSlug.domains.tsx`, `_authed/teams.$teamSlug.links.$linkId.tsx`, `_authed.tsx`, `index.tsx`

- Their `.test.tsx` and `.stories.tsx` siblings.
- `oxlint.config.ts` — one override for `apps/web/src/components/ui/**`.
- `apps/web/package.json` — two font dependencies.
- `CLAUDE.md`, `docs/planning/03-frontend.md`

**Deliberately unchanged:** `copy-button.tsx`, `language-switcher.tsx` and `theme-toggle.tsx` already compose `ui/button.tsx` and need no edit — the last two only change call site, moving from the public header into the sidebar footer.

---

### Task 1: Prove `cn` resolves conflicting Tailwind classes

`apps/web/src/lib/utils.ts` is one line: `export { cn } from 'cn';`. The npm package `cn` (0.2.6) advertises itself as a drop-in replacement for clsx + tailwind-merge. Every generated shadcn component relies on that function **resolving** conflicts, not concatenating: `cn('size-8', className)` must let a caller's `size-10` win. If it only concatenates, the winner is whichever Tailwind emits last in the stylesheet — which is stable enough to look fine in review and wrong in ways nobody traces back here.

This is three lines of test and it gates the whole plan. If it fails, stop and report: the fix is swapping `lib/utils.ts` to `clsx` + `tailwind-merge`, which is a decision, not a detail.

**Files:**

- Create: `apps/web/src/lib/utils.test.ts`

**Interfaces:**

- Consumes: nothing.
- Produces: confidence that `cn(...inputs: ClassValue[]): string` resolves conflicts. Every later task depends on it.

- [ ] **Step 1: Write the test**

```ts
import { describe, expect, it } from 'vitest';

import { cn } from './utils';

/*
 * Every generated `ui/*` component composes its own classes with a caller's
 * through `cn`, so a caller overriding a size, padding or colour depends on
 * the later class *replacing* the earlier one rather than both surviving.
 * The npm package `cn` claims to be a drop-in replacement for
 * clsx + tailwind-merge; this is that claim, checked once, because a
 * concatenating implementation fails silently and only where someone
 * overrides.
 */
describe('cn', () => {
	it('lets a later conflicting utility win', () => {
		expect(cn('size-8', 'size-10')).toBe('size-10');
	});

	it('keeps classes that do not conflict', () => {
		expect(cn('flex', 'items-center')).toBe('flex items-center');
	});

	it('drops falsy values', () => {
		expect(cn('flex', false, undefined, 'gap-2')).toBe('flex gap-2');
	});
});
```

- [ ] **Step 2: Run it**

Run: `pnpm --filter web test -- src/lib/utils.test.ts`

Expected: PASS. If `cn('size-8', 'size-10')` returns `'size-8 size-10'`, **stop the plan and report** — `cn` does not merge and the dependency choice needs revisiting before any component is generated.

- [ ] **Step 3: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "test(web): pin the cn merge guarantee"
```

---

### Task 2: Initialise shadcn from the preset and restructure the tokens

`init` overwrites three files. Two of them carry rules that are not shadcn's and are silent when lost.

**Files:**

- Modify: `apps/web/components.json` (rewritten by `init`)
- Modify: `apps/web/src/styles/app.css` (rewritten by `init`, then repaired)
- Modify: `apps/web/src/components/ui/button.tsx` (regenerated)
- Modify: `apps/web/src/routes/__root.tsx`
- Modify: `apps/web/.storybook/preview.tsx`
- Modify: `oxlint.config.ts`
- Modify: `apps/web/src/routes/__root.test.tsx`

**Interfaces:**

- Consumes: Task 1's `cn` guarantee.
- Produces: `[data-theme='indigo']` and `[data-theme='indigo'].dark` token blocks in `app.css`; `data-theme="indigo"` on `<html>`; a `components/ui/**` oxlint override.

- [ ] **Step 1: Record what must survive**

Before running anything, copy these three rules out of `apps/web/src/styles/app.css` — they are not shadcn's and `init` will delete them:

```css
@custom-variant dark (&:where(.dark, .dark *));

* {
	box-sizing: border-box;
}

html,
body,
#app {
	min-height: 100%;
}

body {
	margin: 0;
}
```

`@custom-variant dark` is the load-bearing one. `themeClassName()` writes a `dark` class onto `<html>`; without this line Tailwind's `dark:` variant stops matching that class and dark mode silently stops working — no error, no failing build, just a light page with a `dark` class on it.

- [ ] **Step 2: Run init**

Run: `cd apps/web && pnpm dlx shadcn@latest init --preset b39ODpImW -b base`

`-b base` is load-bearing — see Global Constraints. Without it the CLI defaults to `radix` and everything else still looks correct.

When it offers to overwrite `src/lib/utils.ts`, **decline**. The existing one-line re-export stays (Task 1 proved it correct), and accepting would add `clsx` and `tailwind-merge` as dependencies for no gain.

**Verify all three before going further** — this is the checkpoint the first run walked past:

1. `components.json` contains `"style": "base-sera"`, not `radix-sera`.
2. `src/components/ui/button.tsx` imports its `Slot` from Base UI, not from `radix-ui`.
3. `grep -rn "radix" apps/web/package.json` finds nothing.

If any of the three is wrong, re-run with `--force` rather than editing `components.json` by hand: the file records what generated the components, and a hand-edited value would claim a primitive layer the components do not actually use.

`--radius` is whatever this command writes. The spec records it as `0`, measured from the web preview; if the CLI writes something else, **keep the CLI's value and report the difference** — the preset is the authoritative record and the spec's transcription is not, so the spec gets amended rather than the token hand-edited.

- [ ] **Step 3: Restore the four non-shadcn rules**

Paste the block from Step 1 back into `apps/web/src/styles/app.css`, keeping `@custom-variant dark` directly beneath the `@import 'tailwindcss';` line and the element rules at the end of the file.

- [ ] **Step 4: Move the colour tokens under `[data-theme='indigo']`**

`init` writes the colours into bare `:root` and `.dark`. Rewrite those two blocks so `:root` keeps only what is not a colour, and the colours move under the attribute:

```css
/*
 * Colour lives under `[data-theme]`, not on bare `:root`, because a colour
 * theme and light/dark are two independent axes: `<html>` carries a `dark`
 * class (see `themeClassName`) *and* a `data-theme` attribute. A settings
 * page offering a second colour theme is planned; structuring the tokens
 * this way now makes that a CSS block instead of a rewrite of every token.
 *
 * The attribute's value is hardcoded to `indigo` in `__root.tsx` for now —
 * there is nothing that can write a different one yet, and a reader with one
 * possible outcome is machinery without a user.
 */
:root {
	--radius: 0;
	/* …every non-colour token init produced: fonts, spacing, shadows… */
}

[data-theme='indigo'] {
	/* …every colour token from init's `:root` block… */
}

[data-theme='indigo'].dark {
	/* …every colour token from init's `.dark` block… */
}
```

Move the tokens verbatim; do not retype the `oklch()` values. The `@theme inline { … }` block that maps `--color-*` to `var(--*)` stays exactly where `init` put it and is not touched — it reads the variables at use time, so it does not care which selector defined them.

- [ ] **Step 5: Put the attribute on `<html>`**

In `apps/web/src/routes/__root.tsx`, `RootDocument` currently renders:

```tsx
<html className={themeClassName(theme)} lang={language}>
```

Change it to:

```tsx
{
	/* `data-theme` is the colour axis, `className` the light/dark one — see the
	   comment above the token blocks in styles/app.css. Hardcoded until a
	   settings page can write a preference; when that arrives it reads a cookie
	   here exactly as `readTheme` already does for `theme`. */
}
<html className={themeClassName(theme)} data-theme="indigo" lang={language}>
```

- [ ] **Step 6: Fix the Storybook decorator**

`apps/web/.storybook/preview.tsx` wraps every story in `<div className={isDark ? 'dark' : undefined}>`. That div is now outside any `[data-theme]`, so **every story would render with no colour tokens at all**. Add the attribute:

```tsx
<div className={isDark ? 'dark' : undefined} data-theme="indigo">
```

- [ ] **Step 7: Add the oxlint override**

`react/forbid-component-props` fires on every generated component that forwards `className` — `ui/button.tsx` carries a hand-written exemption for exactly this today, and Task 4 adds eighteen more files with the same shape. One override replaces all of them. Add to the `overrides` array in `oxlint.config.ts`, keeping the array's existing ordering conventions and `sort-keys` happy:

```ts
{
	// Everything under `ui/` is `shadcn add` output, regenerated by the CLI and
	// never hand-edited (see the design-system spec). Forwarding `className` is
	// the whole contract of those components — `buttonVariants({ className })`,
	// `cn(…, className)` — so the rule has nothing to protect here and its only
	// effect would be an inline exemption in every generated file, which the
	// next `shadcn add` would strip out again.
	//
	// `plugins` has to be repeated: oxlint overrides are not deep-merged, and a
	// `rules` entry only takes effect for a plugin the same override activates.
	files: ['apps/web/src/components/ui/**'],
	plugins: ['react'],
	rules: {
		'react/forbid-component-props': 'off',
	},
},
```

Then delete the now-redundant inline exemption inside `apps/web/src/components/ui/button.tsx` — it is generator output and must match what `shadcn add` would write.

- [ ] **Step 8: Assert the attribute in the root test**

Add to `apps/web/src/routes/__root.test.tsx`, alongside the existing theme-class assertions:

```tsx
it('renders the colour theme attribute on the document element', () => {
	// Without this attribute every colour token is undefined and the whole app
	// renders unstyled — a failure that looks like a CSS build problem rather
	// than a missing attribute, so it is pinned here.
	render(<RootDocument>{null}</RootDocument>);
	expect(document.documentElement).toHaveAttribute('data-theme', 'indigo');
});
```

Match the file's existing render helper and imports rather than introducing new ones; read the surrounding tests first.

- [ ] **Step 9: Verify dark mode still works**

Run: `pnpm --filter web storybook` and toggle the Theme global to `dark` on any existing story. Expected: the background actually changes. If it does not, `@custom-variant dark` did not survive Step 3 or the decorator in Step 6 is missing the attribute.

Then run the gate: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

- [ ] **Step 10: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): init shadcn on base ui tokens"
```

---

### Task 3: Self-host the Geist fonts

The preset names Geist for `--font-sans` and Geist Mono for `--font-mono`. Loading either from a font CDN would transmit every visitor's IP address to that CDN, which for an app aimed at German associations is the practice a Munich court ruled against in 2022 — and `docs/planning/08-legal-and-compliance.md` already owes those visitors a privacy policy that should not have to explain it.

**Files:**

- Modify: `apps/web/package.json`
- Modify: `apps/web/src/styles/app.css`

**Interfaces:**

- Consumes: Task 2's `app.css`.
- Produces: `Geist Variable` and `Geist Mono Variable` families resolvable by the `--font-sans` / `--font-mono` tokens.

- [ ] **Step 1: Add the dependencies**

Run: `pnpm --filter web add @fontsource-variable/geist @fontsource-variable/geist-mono`

Both are at 5.3.0, published 2026-07-19 — comfortably past the workspace's `minimumReleaseAge` gate.

- [ ] **Step 2: Import them**

At the top of `apps/web/src/styles/app.css`, above `@import 'tailwindcss';`:

```css
/*
 * Self-hosted, never a font CDN: a CDN request hands the visitor's IP address
 * to a third party on every page load, which is exactly the transfer the
 * privacy policy this instance owes its Vereine should not have to explain.
 * The preset's `--font-sans` and `--font-mono` name these two families.
 */
@import '@fontsource-variable/geist';
@import '@fontsource-variable/geist-mono';
```

- [ ] **Step 3: Verify the family actually resolves**

Run: `pnpm --filter web dev`, open `http://localhost:3000`, and in the browser console:

```js
getComputedStyle(document.body).fontFamily;
```

Expected: a string beginning with `Geist`. Then check the Network panel: the `.woff2` requests must come from `localhost`, not from any external host. If any font request leaves the origin, the import is wrong — fix it before committing.

- [ ] **Step 4: Run the gate**

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook && pnpm --filter web build`

The build is included here specifically: font assets are the kind of thing that resolves in dev and fails in a production bundle.

- [ ] **Step 5: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): self-host the geist fonts"
```

---

### Task 4: Install the component set

One `shadcn add` run. Nothing speculative: every component on this list is used by a page that exists today, or is pulled in as a dependency of one that is.

**Files:**

- Create: `apps/web/src/components/ui/*` (generator output)

**Interfaces:**

- Consumes: Task 2's `components.json` and tokens, Task 2's oxlint override.
- Produces: `Button`, `Input`, `Label`, `Field`/`FieldLabel`/`FieldDescription`/`FieldError`/`FieldGroup`, `Select`, `Textarea`, `Checkbox`, `Card`/`CardHeader`/`CardTitle`/`CardDescription`/`CardContent`/`CardFooter`, `Table`/`TableHeader`/`TableBody`/`TableRow`/`TableHead`/`TableCell`, `Badge`, `AlertDialog` and its parts, `DropdownMenu` and its parts, `Sidebar` and its parts (`SidebarProvider`, `SidebarTrigger`, `SidebarInset`, `SidebarHeader`, `SidebarContent`, `SidebarFooter`, `SidebarMenu`, `SidebarMenuItem`, `SidebarMenuButton`), `Empty`, `InputGroup`, `Pagination`, `Separator`, `Skeleton`, `Spinner`.

- [ ] **Step 1: Add them**

Run:

```bash
cd apps/web && pnpm dlx shadcn@latest add button input label field select textarea checkbox card table badge alert-dialog dropdown-menu sidebar empty input-group pagination separator skeleton spinner
```

`sidebar` also pulls `sheet`, `tooltip`, `separator` and `skeleton`; accept those.

`add` takes **no** `-b/--base` flag — unlike `init`, it reads the primitive layer out of `components.json`. Verified against `shadcn@latest add --help`. So this step is only as correct as Task 2's verification checkpoint: confirm `components.json` says `"style": "base-sera"` before running it, or nineteen components arrive on the wrong primitive at once.

**Not** added, deliberately: `chart` (arrives with the analytics spec), `data-table` (arrives with list filtering), `calendar` and `date-picker` (the expiry field stays a native `<input type="datetime-local">`).

- [ ] **Step 2: Do not touch what it generated**

Read the diff, change nothing in it. If a component's appearance is wrong, the fix is a token, a wrapper, or a different preset — never an edit inside `ui/`, because the next `shadcn add` silently reverts it and nothing fails when it does.

- [ ] **Step 3: Remove the dead Radix dependency**

`radix-ui` was in `apps/web/package.json` for one import — `Slot` in the old `button.tsx`, which Task 2 replaced. Confirm nothing imports it any more:

Run: `grep -rn "radix-ui\|@radix" apps/web/src`

Expected: no matches. Then: `pnpm --filter web remove radix-ui`

- [ ] **Step 4: Run the gate**

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook && pnpm --filter web build`

`pnpm lint` is the interesting one: if `react/forbid-component-props` findings appear, the Task 2 override's `files` glob or its `plugins` array is wrong.

- [ ] **Step 5: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): add the shadcn component set"
```

---

### Task 5: Build the sidebar shell

`AuthedShell` today is a flex `<header>` holding a `<ul>` navigation, a team switcher and a sign-out button. It becomes a real application shell. The property worth preserving is that it is **presentational**: it takes plain props and a callback, so it renders in a test without a router, a `QueryClient` or a session. `_authed.tsx` keeps all the wiring.

**Files:**

- Create: `apps/web/src/components/app-sidebar.tsx`, `app-sidebar.stories.tsx`, `app-sidebar.test.tsx`
- Modify: `apps/web/src/components/authed-shell.tsx`, `authed-shell.test.tsx`
- Modify: `apps/web/src/components/team-switcher.tsx`, `team-switcher.test.tsx`, `team-switcher.stories.tsx`
- Modify: `apps/web/src/routes/_authed.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `de.json`

**Interfaces:**

- Consumes: Task 4's `Sidebar*`, `DropdownMenu*`, `Separator`, `Button`.
- Produces: `AppSidebar(props: { currentTeamSlug: string | undefined; isMaintainer: boolean; memberships: readonly Membership[]; onSignOut: () => void; signingOut: boolean; theme: Theme })` — the same prop shape `AuthedShell` takes today plus `theme`, which the footer's `ThemeToggle` needs.
- Produces: `AuthedShell` gains the same `theme` prop and a `children` prop.

**Where `theme` comes from, and a control that did not exist before.** `ThemeToggle` and `LanguageSwitcher` are rendered today only by `SiteHeader`, which is on the public pages — so a signed-in visitor currently has **no way to switch theme or language at all**. Putting them in the sidebar footer closes that gap, and it is the one place this plan adds a control rather than restyling one. Source the value the way `index.tsx` already does: `_authed.tsx` calls `usePreferences()` (`src/lib/use-preferences.ts`, which reads the root route's loader data) and passes `theme` down. Do **not** call the hook inside `AppSidebar` — that would make it unrenderable without a router and break the presentational property this task exists to preserve.

- [ ] **Step 1: Add the catalogue keys**

The slim top bar needs a label for its trigger. Add to `en.json` under `nav`:

```json
"toggleSidebar": "Toggle the navigation"
```

and to `de.json`:

```json
"toggleSidebar": "Navigation ein- und ausblenden"
```

- [ ] **Step 2: Write the failing test**

`apps/web/src/components/app-sidebar.test.tsx`.

`authed-shell.test.tsx` already contains everything this needs: a `memberships` fixture (`Verein A` / `verein-a` and `Verein B` / `verein-b`) and a `renderShell(props)` helper that builds a minimal in-memory router, because `TeamSwitcher` needs one in context. **Copy that helper's shape into the new file** — renamed `renderSidebar`, with `theme = 'light'` added to its defaults — rather than importing it across test files or inventing a different one.

```tsx
it('links to every section for the current team', () => {
	renderSidebar({ currentTeamSlug: 'verein-a' });

	expect(screen.getByRole('link', { name: 'Links' })).toHaveAttribute(
		'href',
		'/teams/verein-a/links',
	);
	expect(screen.getByRole('link', { name: 'Domains' })).toHaveAttribute(
		'href',
		'/teams/verein-a/domains',
	);
});

it('renders no section navigation without a resolved team', () => {
	// A signed-in visitor with a stale bookmark to a team they have left reaches
	// this shell with no resolvable slug; the links would have nowhere to point.
	// This is the same guard `AuthedShell` documents today and it is carried over
	// unchanged.
	renderSidebar({ currentTeamSlug: undefined, memberships: [] });

	expect(screen.queryByRole('link', { name: 'Links' })).toBeNull();
});

it('offers a theme control, which the authenticated area never had', () => {
	// `ThemeToggle` was only ever rendered by `SiteHeader` on the public pages,
	// so a signed-in visitor could not change theme at all. The sidebar footer
	// is where that gets fixed, and this pins it.
	renderSidebar({ theme: 'light' });

	expect(screen.getByRole('button', { name: 'Switch to dark mode' })).toBeInTheDocument();
});
```

- [ ] **Step 3: Run it to confirm it fails**

Run: `pnpm --filter web test -- src/components/app-sidebar.test.tsx` Expected: FAIL — the module does not exist.

- [ ] **Step 4: Write `AppSidebar`**

Compose `SidebarHeader` (team switcher), `SidebarContent` (`SidebarMenu` of sections with `LinkIcon` and `GlobeIcon` from lucide), `SidebarFooter` (`LanguageSwitcher`, `ThemeToggle`, the maintainer's "create team" link, and the sign-out `Button`). Carry over `AuthedShell`'s existing guard verbatim — `currentTeamSlug !== undefined && memberships.length > 0` — for both the switcher and the section list, and carry over its docstring's reasoning for why.

Section links use TanStack Router's `Link` inside `SidebarMenuButton asChild`.

- [ ] **Step 5: Rebuild `TeamSwitcher` on `DropdownMenu`**

It is a `<nav><ul>` of links today. It becomes a `DropdownMenu` whose trigger shows the current team's name and whose items are the same `Link`s with the same `onClick={() => remember(membership.slug)}` cookie write and the same `aria-current`. The `remember` helper and its `unicorn/no-document-cookie` exemption comment move across unchanged — the reason they exist has not changed.

Update `team-switcher.test.tsx`: the items now live behind a trigger, so the test opens the menu with `userEvent.click(screen.getByRole('button', { name: /SV Grünwald/ }))` before asserting on the links. Keep every existing assertion; do not weaken the accessible-name checks.

- [ ] **Step 6: Rewrite `AuthedShell` as the shell**

```tsx
<SidebarProvider>
	<AppSidebar {...props} />
	<SidebarInset>
		<header className="flex h-12 items-center gap-2 border-b px-4">
			<SidebarTrigger aria-label={t('nav.toggleSidebar')} />
			<Separator className="h-4" orientation="vertical" />
			<span className="font-semibold">{t('brand')}</span>
		</header>
		{children}
	</SidebarInset>
</SidebarProvider>
```

`AuthedShell` now takes `children`, because `SidebarInset` has to wrap the page content for the layout to work.

**`SidebarInset` IS the `<main>`.** Read `apps/web/src/components/ui/sidebar.tsx` and confirm it before writing anything: the generated component renders `<main data-slot="sidebar-inset">` itself. So `_authed.tsx` must **drop** its own `<main>` wrapper rather than move it inside — keeping both produces `<main>…<main>…</main></main>`, which axe reports as `landmark-no-duplicate-main`, `landmark-main-is-top-level` and `landmark-unique`, and which fails the existing `e2e/links.spec.ts` and `e2e/domains.spec.ts` accessibility assertions on every authenticated page.

Pass the sign-out-failure `role="alert"` paragraph and the `<Outlet />` through as `children` unwrapped (or in a plain `<div>`). Rewrite the comment that used to justify the `<main>`: the reasoning it carried is still true — exactly one `<main>` per document, and a per-page wrapper would be forgotten on the next route added — but the element providing it is now `SidebarInset`, not a wrapper this file writes.

Verify the count rather than trusting the markup: render the shell and assert `document.querySelectorAll('main')` has length 1.

- [ ] **Step 7: Add the stories, light and dark**

`apps/web/src/components/app-sidebar.stories.tsx`, following `copy-button.stories.tsx`'s shape. Two stories, because the theme is a Storybook global whose default is `light` — the Vitest addon runs each story with the default, so a dark story is the only way dark is actually exercised in CI:

```tsx
export const Default: StoryObj<typeof meta> = {
	args: {
		currentTeamSlug: 'verein-a',
		isMaintainer: false,
		memberships: [{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' }],
		onSignOut: () => undefined,
		signingOut: false,
		theme: 'light',
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand. `args.theme` is what ThemeToggle
// renders from; `globals.theme` is what preview.tsx's decorator reads to add
// the `dark` class. Both are needed, or the story is half dark.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Default.args, theme: 'dark' },
	globals: { theme: 'dark' },
};
```

**Verify per-story `globals` actually works before repeating this in five more files.** Run `pnpm --filter web test:storybook` and confirm the `Dark` story renders on a dark background — a story-level `globals` override is Storybook 8+ behaviour and this project is on Storybook 10, but it is one command to check and six files to redo if it silently does nothing. If it does not take effect, the fallback is a per-story decorator that applies the `dark` class itself; take that route and note it here.

- [ ] **Step 8: Run the tests**

Run: `pnpm --filter web test -- src/components/app-sidebar.test.tsx src/components/authed-shell.test.tsx src/components/team-switcher.test.tsx` Expected: PASS.

Then the full gate: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

- [ ] **Step 9: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): replace the header with a sidebar"
```

---

### Task 6: Move the link list onto Table

**Files:**

- Modify: `apps/web/src/components/link-list.tsx`, `link-list.test.tsx`, `link-list.stories.tsx`
- Modify: `apps/web/src/i18n/locales/en.json`, `de.json`

**Interfaces:**

- Consumes: Task 4's `Table*`, `Badge`, `Empty`, `Pagination*`.
- Produces: nothing other tasks read.

- [ ] **Step 1: Add the column-header keys**

A `<table>` needs headers, and the `<ul>` had none. Add under `links` in `en.json`:

```json
"columnShortUrl": "Short link",
"columnDestination": "Destination",
"columnActions": "Actions"
```

and in `de.json`:

```json
"columnShortUrl": "Kurzlink",
"columnDestination": "Ziel",
"columnActions": "Aktionen"
```

- [ ] **Step 2: Update the test to the new roles**

`link-list.test.tsx` currently finds list items. Rewrite those assertions to table roles — **to the new roles, not to whatever still passes**. A `getByText` fallback where a `getByRole` used to be is a test that stopped checking structure.

The file already has `link(overrides): ApiLink`, `pageOf(overrides): PageLink` and `renderWith(data: PageLink, page = 1)`, which builds a minimal in-memory router and renders with `teamSlug="verein-a"`. Use those; do not add new fixtures.

```tsx
it('renders one row per link', () => {
	renderWith(pageOf({ items: [link(), link({ id: 'link-2', slug: 'def456' })], total_count: 2 }));

	// One header row plus one per link.
	expect(screen.getAllByRole('row')).toHaveLength(3);
	expect(screen.getByRole('columnheader', { name: 'Short link' })).toBeInTheDocument();
});

it('marks a password-protected link', () => {
	renderWith(pageOf({ items: [link({ has_password: true })], total_count: 1 }));

	// The badge carries text, not only an icon and a colour: colour alone may
	// never be the sole carrier of meaning (WCAG 1.4.1), and the icon is
	// aria-hidden.
	expect(screen.getByText('Password protected')).toBeInTheDocument();
});
```

- [ ] **Step 3: Run it to confirm it fails**

Run: `pnpm --filter web test -- src/components/link-list.test.tsx` Expected: FAIL — no `row` roles yet.

- [ ] **Step 4: Rewrite the markup**

- The `<ul>`/`<li>` becomes `Table` › `TableHeader`/`TableRow`/`TableHead` (the three new keys) and `TableBody`/`TableRow`/`TableCell`.
- The lock indicator becomes `<Badge variant="secondary">` holding `<LockIcon aria-hidden />` plus the existing `links.passwordBadge` text. The text stops being `sr-only` — a badge that reads as an icon to sighted users and as text to everyone else is two different interfaces.
- The empty state becomes `Empty` with the existing `links.empty` copy and the existing create link.
- The pagination `<nav>` becomes `Pagination`, keeping the existing `aria-disabled` treatment for the unavailable direction rather than removing the control.
- `CopyButton` keeps its current call site and needs no change — it already composes `ui/button.tsx`.
- `short-url-notice.tsx` keeps its markup and its `.invalid`-suffix logic exactly as they are, and gains token classes so it reads as a warning: `border-destructive/50 bg-destructive/10 text-foreground` on its wrapper, with an `aria-hidden` `TriangleAlertIcon`. No `Alert` component is installed for this one banner, and its existing docstring — which explains why the suffix check is better than a feature flag — stays.

Everything else in the file — `items = data.items ?? []`, the `hasNextPage` arithmetic, the `.invalid` hostname search and its comment — is untouched.

- [ ] **Step 5: Add the dark story**

Add a `Dark` story to `link-list.stories.tsx` in the shape given in Task 5, Step 7.

- [ ] **Step 6: Run the tests, then the gate**

Run: `pnpm --filter web test -- src/components/link-list.test.tsx` Expected: PASS.

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

- [ ] **Step 7: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): render the link list as a table"
```

---

### Task 7: Move the domain list onto Table and Badge

`domain-list.tsx` already uses a real `<table>`, so this is mostly styling — with one substantive rule.

**Files:**

- Modify: `apps/web/src/components/domain-list.tsx`, `domain-list.test.tsx`, `domain-list.stories.tsx`

**Interfaces:**

- Consumes: Task 4's `Table*`, `Badge`.
- Produces: nothing other tasks read.

- [ ] **Step 1: Write the status-badge test**

The file already has `domain(overrides): ApiDomain` (defaulting to `verification_status: 'pending'`), the derived `pendingDomain` / `verifiedDomain` constants, a `RenderOverrides` interface, and `renderList(domains: ApiDomain[], overrides: RenderOverrides = {})`. Use those.

```tsx
it.each([
	['pending', 'Waiting for DNS'],
	['verified', 'Working'],
	['failed', 'Another team verified this hostname first'],
] as const)('states %s in words, not only in colour', (status, label) => {
	// WCAG 1.4.1: colour is never the only carrier of meaning. The badge's icon
	// is aria-hidden, so the text is what every reader actually gets.
	renderList([domain({ verification_status: status })]);

	expect(screen.getByText(label)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run it**

Run: `pnpm --filter web test -- src/components/domain-list.test.tsx` Expected: PASS already if the existing markup renders those strings — in which case the test is a regression guard, which is the point. If it fails, the status is currently rendered some other way and the rewrite in Step 3 must produce these strings.

- [ ] **Step 3: Rewrite the markup**

- `<table>`/`<thead>`/`<tbody>`/`<tr>`/`<th>`/`<td>` become `Table`/`TableHeader`/`TableBody`/`TableRow`/`TableHead`/`TableCell`.
- The status cell becomes a `Badge` carrying an `aria-hidden` lucide icon (`ClockIcon`, `CheckIcon`, `XIcon`) **and** the existing translated text. Variant by status; the text is never removed.
- The DNS-record instructions keep their `CopyButton`s and their existing structure.
- The `<ul>` in this file becomes the record list inside the instructions block; keep its semantics.

- [ ] **Step 4: Add the dark story, run the gate, commit**

Add a `Dark` story as in Task 5, Step 7.

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): restyle the domain list"
```

---

### Task 8: Move the link forms onto Field

Three files, one pattern. Each currently hand-wires `htmlFor`, `aria-describedby`, `aria-invalid` and an error-id per field. `Field` owns all four.

**Files:**

- Modify: `apps/web/src/components/link-form.tsx`, `link-form.test.tsx`, `link-form.stories.tsx`
- Modify: `apps/web/src/components/link-password-card.tsx`, `link-password-card.test.tsx`, `link-password-card.stories.tsx`
- Modify: `apps/web/src/components/link-qr-card.tsx`, `link-qr-card.test.tsx`, `link-qr-card.stories.tsx`

**Interfaces:**

- Consumes: Task 4's `Field`, `FieldLabel`, `FieldDescription`, `FieldError`, `FieldGroup`, `Input`, `Select`, `Checkbox`, `InputGroup`, `Card*`.
- Produces: the `Field` idiom that Task 9 repeats.

- [ ] **Step 1: Establish the pattern on one field**

Each `form.Field` render prop becomes:

```tsx
<form.Field name="destination_url">
	{(field) => (
		<Field data-invalid={errorMessage !== undefined}>
			<FieldLabel htmlFor={field.name}>{t('links.destination')}</FieldLabel>
			<Input
				id={field.name}
				name={field.name}
				onBlur={field.handleBlur}
				onChange={(event) => field.handleChange(event.target.value)}
				value={field.state.value}
			/>
			{errorMessage === undefined ? null : <FieldError>{errorMessage}</FieldError>}
		</Field>
	)}
</form.Field>
```

`FieldError` renders `role="alert"` itself, so the hand-written `<p id={errorId} role="alert">` and its `aria-describedby` wiring are deleted rather than kept alongside. Verify that claim by reading the generated `ui/field.tsx` before deleting anything — if it does not, keep the explicit `role="alert"`.

- [ ] **Step 2: Keep these three things exactly as they are**

- The **expiry field stays** `<input type="datetime-local">`, wrapped in `Field` but not replaced by a date picker.
- The **301 warning** (`links.redirect301Warning`) keeps rendering when 301 is selected, and moves into `FieldDescription`.
- The **QR colour inputs** become `InputGroup` with a leading `#` addon, but the hex validation, the `qr-contrast.ts` check and the `links.qrLowContrast` / `links.qrInvalidColor` messages are untouched.

- [ ] **Step 3: Update the tests to the new roles**

Label association changes shape, so `getByLabelText` queries may need adjusting — **adjust them, do not replace them with `getByPlaceholderText` or a test id**. Every assertion that checked an accessible name must still check an accessible name. Add, if not already present:

`link-form.test.tsx` already has `renderForm(props)`, and its props include a `fieldErrors?: Readonly<Record<string, string>>` — so the error state needs no interaction to reach.

```tsx
it('associates each error with its field', () => {
	// The whole reason for moving to `Field`: the association used to be four
	// hand-written attributes per field (`htmlFor`, `id`, `aria-describedby`,
	// `aria-invalid`), and a missed one is invisible until a screen reader hits
	// it. `Field` owns all four, so this asserts the outcome rather than the
	// attributes.
	renderForm({
		fieldErrors: { destination_url: 'Destination URL is required.' },
		onSubmit: vi.fn<(values: LinkFormValues) => void>(),
	});

	expect(screen.getByLabelText('Destination URL')).toHaveAccessibleDescription(
		'Destination URL is required.',
	);
});
```

- [ ] **Step 4: Wrap the two cards**

`link-password-card.tsx` and `link-qr-card.tsx` become `Card` › `CardHeader`/`CardTitle`/`CardDescription` › `CardContent`, using their existing `links.passwordHeading` / `links.passwordExplainer` and `links.qrHeading` / `links.qrExplainer` copy. No copy changes.

- [ ] **Step 5: Add dark stories, run the gate, commit**

Add a `Dark` story to each of the three story files as in Task 5, Step 7.

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): move the link forms onto Field"
```

---

### Task 9: Move the remaining forms onto Field and restyle the public shell

Same pattern as Task 8, applied to the three forms outside the link area — plus the pages that never enter the sidebar at all.

**Files:**

- Modify: `apps/web/src/routes/login.tsx`, `login.test.tsx`, `login.stories.tsx`
- Modify: `apps/web/src/routes/_authed/new-team.tsx`, `new-team.test.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.domains.tsx`
- Modify: `apps/web/src/components/site-header.tsx`, `site-footer.tsx`, `site-footer.test.tsx`
- Modify: `apps/web/src/routes/index.tsx`, `__root.tsx` (the `NotFound` component only)

**Interfaces:**

- Consumes: Task 8's `Field` idiom, Task 4's `Card*`, `Separator`, `Badge`.
- Produces: nothing other tasks read.

- [ ] **Step 1: Apply the Field idiom**

Use the exact shape from Task 8, Step 1 for each field: the email field in `login.tsx`; the name and slug fields in `new-team.tsx` (the slug's `teams.slugHint` becomes `FieldDescription`); the hostname field in the domain claim form (`domains.hostnameHint` becomes `FieldDescription`).

- [ ] **Step 2: Give the login form a Card**

`login.tsx` is a centred form on an otherwise empty page. Wrap it in `Card` with `CardTitle` from `auth.signInTitle`. The `auth.linkSent` confirmation keeps its current `role` and copy — it is deliberately worded not to reveal whether the address exists, and the enumeration guarantee it belongs to is enforced in `server/auth.ts`. Do not touch either.

- [ ] **Step 3: Update the tests to the new roles**

As in Task 8, Step 3: adjust label queries, keep every accessible-name assertion. `new-team.test.tsx` has assertions about the slug error messages and the `teams.slugTaken` 409 path — those are behaviour and must keep passing unchanged.

- [ ] **Step 4: Restyle the public shell**

The landing page, login and the not-found route never reach the sidebar — they keep `SiteHeader` and `SiteFooter`, because they have no team context and nothing to navigate between. They are not left alone, though: they are the first thing a Verein sees.

- `site-header.tsx` gets the same slim treatment as the authenticated top bar — `h-12`, `border-b`, the brand at `font-semibold`, `LanguageSwitcher` and `ThemeToggle` on the right — so the two shells read as one product rather than two.
- `site-footer.tsx` keeps its `footer.tagline` and its API-status line. The status becomes a `Badge`: `ok` neutral, `unreachable` destructive, **always with its existing text**, because `fetchHealth` degrades to `unreachable` rather than throwing and a colour-only indicator would say nothing to a screen reader. `site-footer.test.tsx`'s existing assertions on the status text must keep passing unchanged.
- `index.tsx`'s two branches (`marketing` and `noTeam`) keep their exact copy and their `buttonVariants` links — those already carry a reasoned `react/forbid-component-props` exemption each, and both stay. Only spacing and type scale change.
- `__root.tsx`'s `NotFound` keeps its heading and body copy; only its classes change.

Do not touch `resolveHomeOutcome`, `fetchCurrentUser`, the loader, or the redirect — `index.test.ts` covers all four and none of them is presentation.

- [ ] **Step 5: Add a dark login story, run the gate, commit**

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): restyle the auth and public pages"
```

---

### Task 10: Turn ConfirmDelete into a real dialog

`ConfirmDelete` today swaps itself for a `<div role="alertdialog">` in place — correct roles, but no focus trap, no Escape handling, and nothing restoring focus afterwards. `AlertDialog` provides all three.

**Files:**

- Modify: `apps/web/src/components/confirm-delete.tsx`, `confirm-delete.test.tsx`, `confirm-delete.stories.tsx`
- Modify: `apps/web/src/routes/_authed/teams.$teamSlug.links.$linkId.tsx`

**Interfaces:**

- Consumes: Task 4's `AlertDialog` and its parts, `Card*`.
- Produces: `ConfirmDelete` keeps its current props exactly — `{ confirmLabel?: string; label: string; onConfirm: () => void; question: string }`. Every call site is unchanged.

- [ ] **Step 1: Write the focus test**

The file already has `renderWith(onConfirm: () => void, question?: string)`, which renders `ConfirmDelete` with `label="Delete"` inside an `I18nextProvider`. Use it.

```tsx
it('returns focus to the trigger when dismissed', async () => {
	// The in-place version had no focus management at all: cancelling unmounted
	// the trigger and restored focus to nothing, dropping a keyboard user at the
	// top of the document. Every existing test in this file still has to pass —
	// the arming behaviour they cover is the point of the component and is not
	// what changes here.
	const user = userEvent.setup();
	renderWith(vi.fn<() => void>());

	const trigger = screen.getByRole('button', { name: 'Delete' });
	await user.click(trigger);
	await user.click(screen.getByRole('button', { name: 'Cancel' }));

	expect(trigger).toHaveFocus();
});
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `pnpm --filter web test -- src/components/confirm-delete.test.tsx` Expected: FAIL — the current implementation unmounts the trigger and restores nothing.

- [ ] **Step 3: Rewrite on AlertDialog**

`AlertDialogTrigger` wraps the `label` button; `AlertDialogContent` holds `AlertDialogTitle` (the `question`), `AlertDialogAction` (`confirmLabel ?? t('links.deleteConfirm')`, calling `onConfirm`) and `AlertDialogCancel` (`t('links.cancel')`). The `useState`/`useId` pair and the `armed` branch go away — the component owns that now. The exported props do not change.

- [ ] **Step 4: Card the link detail sections**

In `teams.$teamSlug.links.$linkId.tsx`, the page's sections become `Card`s consistent with Task 8's password and QR cards. Loaders, mutations and error handling are untouched.

- [ ] **Step 5: Run the tests, then the gate, then commit**

Run: `pnpm --filter web test -- src/components/confirm-delete.test.tsx` Expected: PASS.

Run: `pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook`

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "feat(web): make delete confirmation a dialog"
```

---

### Task 11: Measure the contrast and record the numbers

The Sera style draws input fields with an underline rather than a box, which makes the focus indicator the primary affordance rather than a secondary one. WCAG 1.4.11 asks 3:1 for non-text contrast. Storybook's axe run will catch text contrast per story; it does not tell anyone what the baseline is, and a number nobody wrote down is a number nobody can notice drifting.

**Files:**

- Create: `docs/superpowers/plans/2026-09-13-design-system-contrast.md`

**Interfaces:**

- Consumes: Tasks 2–10.
- Produces: a recorded baseline referenced by the spec's testing section.

- [ ] **Step 1: Read the six pairs**

Run `pnpm --filter web dev`, open the app, and for **each of light and dark** read the computed values:

```js
const cs = getComputedStyle(document.documentElement);
[
	'--background',
	'--foreground',
	'--primary',
	'--primary-foreground',
	'--muted-foreground',
	'--ring',
].map((k) => [k, cs.getPropertyValue(k)]);
```

- [ ] **Step 2: Compute the ratios**

For each mode, compute the contrast ratio of: `foreground` on `background`; `primary-foreground` on `primary`; `muted-foreground` on `background`; and `ring` on `background`. The first three must reach **4.5:1** (WCAG 1.4.3, normal text). The fourth must reach **3:1** (WCAG 1.4.11, non-text).

- [ ] **Step 3: Write them down**

Create `docs/superpowers/plans/2026-09-13-design-system-contrast.md` with a table of the eight measurements, each marked pass or fail against its threshold, and the date.

- [ ] **Step 4: Act on any failure**

A failing pair is a real finding, not a rounding problem. Report it with the numbers rather than adjusting a token to make it go away — changing a preset token by hand reintroduces exactly the drift this plan's `ui/` rule exists to prevent, and the right answer may be a different Theme colour.

- [ ] **Step 5: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "docs: record the palette contrast baseline"
```

---

### Task 12: Update the documentation

Two documents assert things this work makes false.

**Files:**

- Modify: `CLAUDE.md`
- Modify: `docs/planning/03-frontend.md`

**Interfaces:**

- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Fix the CLAUDE.md stack table**

The UI row reads:

> | UI | **shadcn/ui on Radix** (`-b radix`), Tremor for analytics, lucide-react | Radix deliberately, _not_ the new Base UI default — Tremor is Radix-based |

Replace it with a row naming **shadcn/ui on Base UI** (style `base-sera`, preset `b39ODpImW`), shadcn `chart` on Recharts for analytics, and lucide-react — with a note that Base UI replaced Radix on 2026-09-13 once Tremor was dropped.

- [ ] **Step 2: Add the two load-bearing rules to CLAUDE.md**

Under the non-obvious constraints, add that files under `apps/web/src/components/ui/` are `shadcn add` output and are never hand-edited (a hand edit survives until the next `shadcn add` and then vanishes silently), and that colour tokens live under `[data-theme='indigo']` rather than `:root` because colour and light/dark are two independent axes on `<html>`.

- [ ] **Step 3: Replace the primitive-layer section in 03-frontend.md**

The section "Primitive layer: Radix, not Base UI" is replaced by one recording the reversal: the condition that document itself named for revisiting was met, and the measured cost was near zero — Tremor never installed anywhere in the repository, Radix reduced to a single `Slot` import, and shadcn's own `chart` component shipping for Base UI on Recharts v3 with no primitive dependency of its own. Keep the original reasoning visible as the history it is; do not delete the argument, replace its conclusion.

- [ ] **Step 4: Update the Decided table and add the font decision**

In the same file, the Tremor row becomes shadcn `chart` on Recharts. Add the font decision: Geist and Geist Mono, self-hosted through `@fontsource-variable/*` rather than a CDN, because a CDN request hands the visitor's IP address to a third party — a privacy constraint, not a styling preference.

- [ ] **Step 5: Commit**

```bash
pnpm format && but commit -b feat/design-system-and-shell -m "docs: record the base ui switch"
```

---

## Finishing

Run the complete gate one last time across the whole branch:

```bash
pnpm lint && pnpm format:check && pnpm typecheck && pnpm --filter web test && pnpm --filter web test:storybook && pnpm --filter web build
```

Then run the Playwright suite against a preview, since the shell change moves every landmark on every authenticated page and `landmark-one-main`, `region` and focus order are exactly what it checks.

Then use `superpowers:finishing-a-development-branch`.
