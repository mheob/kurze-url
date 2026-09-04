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

**Caveat, unverified until a human checks it once:** `vercel.json` currently names the related project by its Vercel project _name_ (`"url-shortener-api"`), matching the string already load-bearing in `api.ts`. Doc 07's own text describes this field as the API project's Vercel project **ID** (`prj_...`), and Vercel's own documented example (vercel.com/docs/monorepos, "Define Related Projects") uses an ID too — there is no confirmed source for a plain name resolving. **The test is the health footer** `SiteFooter` renders (`footer.apiStatus`, sourced from `fetchHealth()` in `src/server/health.ts`): open a preview deployment of `apps/web` for any PR and look at it.

- Reports **`ok`** → Related Projects resolved correctly; nothing to change.
- Reports **`unreachable`**, while the matching `apps/api` preview for the same PR is itself healthy → the name did not resolve. Go to the `apps/api` Vercel project's Settings → General → Project ID, copy the `prj_...` value, and replace `"url-shortener-api"` with it in `apps/web/vercel.json`.

### Handoff checklist (Vercel dashboard access required)

None of the following can be done from a coding session without dashboard access. In order:

1. **Create a second Vercel project** against this same GitHub repository (the first, for `apps/api`, already exists).
2. **Root Directory**: `apps/web`.
3. **Framework Preset**: confirm it lands on TanStack Start, not a generic Vite/"Other" preset (should auto-detect from `vercel.json` + `package.json`).
4. **Project name**: any name works for `apps/web` itself — it does not need to match `url-shortener-api` or anything already committed. Once chosen, immediately do the Related Projects check above before relying on any preview.
5. **Build settings**: leave build command / output directory on the framework defaults; TanStack Start's Vercel preset handles them.
6. **No Ignored Build Step** for this project — it's a pnpm workspace member, so Vercel's automatic "skip unaffected builds" already covers it.
7. **Environment variables** (Preview + Production): Supabase URL + anon key. `API_HOST` is optional — a local-dev-only fallback; Related Projects supplies the API URL on Vercel itself.
8. **Confirm "deployment_status Events" stays enabled** for this project, Settings → Git (Vercel's default). `.github/workflows/ci-js.yml`'s `e2e` job only fires off this event; if it's ever disabled, E2E silently stops running against previews.
9. **After both projects exist**, open a PR and confirm, in order: the `web` job passes, a preview actually deploys, the `e2e` job fires off that deployment's `deployment_status` success event and passes, and the health footer (above) reports `ok`. Also re-verify the `e2e` job's own `environment`-string project filter (see the comments in `ci-js.yml`) against the real payload the first time both previews exist side by side — it is sourced from a third-party action's documentation, not observed directly against this repo yet.
