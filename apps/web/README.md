# @kurze-url/web

The public-facing frontend of kurze.url — a shared URL shortener for German Vereine. TanStack Start (React), server-rendered, i18n and theme-aware from the first paint. See the repo root's `CLAUDE.md` and `docs/planning/` for the project's full scope; this file only covers what lives in `apps/web`.

## Getting started

```bash
pnpm install
pnpm --filter @kurze-url/web dev   # http://localhost:3000
```

Other scripts (run from `apps/web`, or via `pnpm --filter @kurze-url/web <script>` from the repo root):

| Script | What it does |
| --- | --- |
| `dev` | Vite dev server, port 3000 |
| `build` / `start` | Production build, then serve it (`vite preview`), port 3000 |
| `test` | Vitest, `unit` project only — jsdom, RTL, MSW (`src/**/*.test.ts(x)`) |
| `test:storybook` | Vitest, `storybook` project — every story rendered in real Chromium via `@storybook/addon-vitest`, gated on accessibility |
| `test:watch` | `test`, in watch mode |
| `storybook` / `build-storybook` | Storybook dev server (port 6006) / static build |
| `generate-routes` | Regenerates `src/routeTree.gen.ts` from `src/routes/**` (also runs automatically via the Vite plugin during `dev`/`build`) |

The root also has `pnpm format`, `pnpm lint`, `pnpm typecheck`, all of which cover this package (oxfmt/oxlint, not Prettier/ESLint — see the root `CLAUDE.md`). Playwright is invoked directly, not through a package.json script: `pnpm --filter @kurze-url/web exec playwright test`.

## Architecture

TanStack Start renders every page on the server first; there is no client data-fetching for the initial view. Two mechanisms make that isomorphic-safe:

- **Server functions** (`createServerFn`) wrap anything that needs request-only APIs — reading cookies via `getRequestHeader`, or calling the Go API. On the first SSR pass these run in-process; on a client-side navigation they become a regular RPC to this app's own server. The browser never talks to the Go API directly (see `src/server/api.ts` and `src/server/health.ts`) — that removes the CORS question and keeps a future access token in an httpOnly cookie the browser cannot read.
- **The root route's loader** (`src/routes/__root.tsx`) reads the `lang` and `theme` cookies (falling back to the request's `Accept-Language` header, and then to English/light, if no cookie exists yet — see `src/lib/preferences.ts`) before anything renders, so the first byte of HTML already has the right `<html lang>` and dark/light class. Route components read the result via `useLoaderData()` (or the `usePreferences()` hook, `src/lib/use-preferences.ts`) — never from `localStorage`, which the server cannot see.

### i18n

`react-i18next`, with two flat JSON catalogues under `src/i18n/locales/{en,de}.json`. `src/i18n/index.ts` builds a **fresh i18next instance per request** (`createI18n`) — the server renders for many people in one process, so a shared instance would leak one request's language into another's render. Every user-facing string, including attributes like `aria-label`, must have a key in **both** catalogues; `catalogues.test.ts` (key parity, no-English-shaped-German) and `e2e/i18n.spec.ts` (no rendered string identical across languages, checked against `/` and a 404 path) are what enforce it — `react/jsx-no-literals` (oxlint) only sees JSX text children, not everything.

### Theming

A `theme` cookie (`light` | `dark`), applied as a plain `dark` class on `<html>` (`src/lib/preferences.ts`'s `themeClassName`), which `src/styles/app.css`'s `@custom-variant dark (&:where(.dark, .dark *))` keys off. `ThemeToggle` (`src/components/theme-toggle.tsx`) writes the cookie and flips the class immediately for an instant feel, and keeps its own local state (seeded from the loader's initial value) so repeated clicks keep working without a reload.

### Directory layout

```
src/
  components/       Shell UI: SiteHeader/SiteFooter, LanguageSwitcher,
                     ThemeToggle, and shadcn/ui primitives under ui/
  i18n/              createI18n, documentTitle, and the en/de catalogues
  lib/               preferences (cookies + Accept-Language), utils (cn)
  routes/            __root.tsx (shell + notFoundComponent), index.tsx
  server/            api.ts (Go API client, Related Projects), health.ts
  styles/            app.css — Tailwind v4 tokens + shadcn, on Radix
  test/              Vitest setup (jest-dom, MSW) shared by the `unit` project
e2e/                 Playwright specs (i18n divergence, shell + axe)
.storybook/          Storybook config; addon-a11y + addon-vitest
```

`components.json` is shadcn's config (`-b radix`, deliberately not the newer Base UI default — Tremor, planned for analytics, is Radix-based). Add new primitives with `pnpm dlx shadcn@latest add <component>` from `apps/web`, and prefer them over hand-rolled `<button>`s — see `src/components/ui/button.tsx`.

## Testing strategy

Three layers, each catching what the one below it cannot:

1. **Vitest** (`unit` project) — component/unit logic in jsdom, with RTL and MSW (`src/test/setup.ts`; MSW's handler list starts empty and fails loudly on an unmocked request, rather than silently passing one through).
2. **Storybook + `@storybook/addon-vitest`** (`storybook` project, same `vitest.config.ts`, a separate named project so `pnpm test` never has to boot a real browser) — every story rendered in actual Chromium via `@vitest/browser-playwright`, with `.storybook/preview.tsx`'s `a11y: { test: 'error' }` failing the run on any axe violation. `build-storybook` alone is only a build — it does not inspect rendered output, which is why this project exists as a second, real check.
3. **Playwright + axe-core** (`e2e/`) — full SSR + hydration, against either a local build (`playwright.config.ts`'s `webServer` fallback) or, in CI, a real Vercel preview (`BASE_URL` env var). Cookies in these specs are always scoped to the fixture's `baseURL`, never a hardcoded `localhost` — a cookie scoped to the wrong host is silently never sent, so an axe check against the default light/English render would pass while claiming to test German dark mode.

## Deploying to Vercel

`apps/web` and `apps/api` are two separate Vercel projects from this one monorepo (Root Directory = `apps/web`, Framework Preset auto-detected as TanStack Start). `apps/web/vercel.json` declares `apps/api` as a **Related Project**, so `@vercel/related-projects` (`src/server/api.ts`) can resolve the _matching_ preview (or production) API URL automatically per deployment — without it, a PR that touches both apps would have no way to know the other preview's URL short of hardcoding it. Doc reference: `docs/planning/07-repo-structure-and-tooling.md:49`.

Both projects now exist: **`kurze-url-api`** and **`kurze-url-web`**.

**One thing still unverified, and it has a built-in detector.** `vercel.json` names the related project by its Vercel project _name_ (`"kurze-url-api"`). Doc 07 (`docs/planning/07-repo-structure-and-tooling.md:49`) describes this field as the project **ID** (`prj_...`), and Vercel's own documented example (vercel.com/docs/monorepos, "Define Related Projects") uses an ID. There is no confirmed source for a plain name resolving, and no way to settle it without a deployment.

You do not need to reason about it — **the health footer answers it**. `SiteFooter` renders `footer.apiStatus`, sourced from `fetchHealth()` in `src/server/health.ts`. Open any PR's `kurze-url-web` preview and read it:

- **`ok`** → Related Projects resolved. Nothing to change.
- **`unreachable`**, while that PR's `kurze-url-api` preview is itself healthy → the name did not resolve. Copy the `prj_...` value from `kurze-url-api` → Settings → General → Project ID and replace `"kurze-url-api"` in `apps/web/vercel.json` with it.

That distinction matters: `unreachable` when the API preview is _also_ down means the API is down, not that this wiring is wrong.

### Vercel project settings

Both projects exist. These are the settings `kurze-url-web` needs, and what to confirm once a PR runs.

1. **Root Directory**: `apps/web`.
2. **Framework Preset**: TanStack Start, not a generic Vite or "Other" preset — it should auto-detect.
3. **Build settings**: framework defaults. TanStack Start's Vercel preset handles the build command and output directory.
4. **No Ignored Build Step.** `apps/web` is a pnpm workspace member, so Vercel's automatic "skip unaffected builds" already covers it. (`apps/api` is Go, outside the workspace graph, and does need one — that is its project's concern, not this one's.)
5. **Environment variables** (Preview and Production): the Supabase URL and anon key. `API_HOST` is optional and local-dev only — Related Projects supplies the API URL on Vercel itself.
6. **Confirm "deployment_status Events" is enabled**, Settings → Git. It is Vercel's default. The `e2e` job in `.github/workflows/ci-js.yml` fires off this event alone; if it is ever disabled, E2E stops running against previews and nothing says so.

### Confirm on the first pull request

In order:

1. The `web` job passes.
2. A `kurze-url-web` preview deploys.
3. The `e2e` job fires off that deployment's `deployment_status` success event and passes.
4. The health footer reports `ok` (see above).

**Also verify the `e2e` job's project filter against the real payload the first time both previews exist side by side.** It matches deployments whose `environment` ends with `– kurze-url-web`, and that suffix format is sourced from a Vercel-org action's documentation rather than observed against this repo. The filter is a positive match, so it fails closed — if the format differs, the job stops running rather than running against the API. To see the actual string, add a step running `echo '${{ toJson(github.event.deployment) }}'` temporarily.
