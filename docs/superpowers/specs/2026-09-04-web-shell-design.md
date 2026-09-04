# The Web Shell — Design

**Status:** approved 2026-09-04 **Supersedes nothing. Amends:** `docs/planning/03-frontend.md` (silent on where the active language lives, and on how the browser reaches the API).

This is the fifth implementation spec for the URL shortener, and the first for the frontend. It builds on four merged plans, all backend:

- **Plan 1** — foundation and the redirect path.
- **Plan 2** — tenancy, authorization and audit.
- **Plan 3** — links and the shared domain.
- **Plan 4** — folders and tags.

Those four shipped 24 operations and a generated TypeScript SDK, and nothing consumes them. `apps/web` holds a `.gitkeep`.

This spec does not build the product. It builds the shell the product will live in, because three of the project's rules — i18n from the first component, dark and light mode from the first component, and accessibility checked in CI at two levels — are all far cheaper to establish before the first real page than to retrofit after. `docs/planning/03-frontend.md` says so explicitly about i18n: "worth setting up the pattern before the first real page is built rather than after."

---

## Goal

A deployed, bilingual, themed, accessible page that proves the structure works, so that every plan after this one can add product surface without relitigating structure.

---

## Scope

### In scope

- The TanStack Start application: routing, SSR, Tailwind, `shadcn init -b radix`, lucide-react.
- `react-i18next` wired SSR-safe, English and German both real from the first string.
- Dark and light mode, chosen by the viewer, with no flash of the wrong theme.
- One public page: header with a language switcher and a theme toggle, translated copy, footer, and a sign-in button that is a visible stub.
- One server function calling `GET /v1/health`, to prove the deployment path end to end.
- Storybook with the accessibility addon.
- Vitest + React Testing Library + MSW, and Playwright + `@axe-core/playwright`.
- The `apps/web` Vercel project, with Related Projects pointing at the API project.
- CI: the JS workflow extended for the new app; E2E against the pull request's Vercel preview.

### Out of scope, and where each lands

- **Authentication.** Plan 6. Supabase OAuth 2.1 with PKCE, an SSR-safe session, and the httpOnly cookie this spec's architecture is chosen to permit.
- **Any authenticated route, and team context.** Plan 6.
- **Links, folders, tags.** Plan 7.
- **Members, invitations, the audit log.** Plan 8.
- **Tremor.** There is nothing to chart until analytics exist.
- **Marketing copy.** The public page proves the structure; describing a product that does not yet exist would be rewritten the moment it does.
- **Legal pages.** `Impressum` and `Datenschutzerklärung` are an open item in `CLAUDE.md` awaiting a lawyer. Shipping the routes with placeholder text would be worse than not shipping them: these are the two documents whose accuracy is the entire point.
- **A hosted Storybook.** Nothing asks for one, and it is a service to maintain.

---

## Global constraints

These bind every task. They are the project's rules, restated here so the plan's executors do not have to reconstruct them from `CLAUDE.md`.

- The tenant is called `team` in every identifier. "Verein" appears only in user-facing German copy.
- **No hardcoded user-facing string, anywhere, ever — not even temporarily.** English is the default, German ships alongside it.
- Dark and light mode from the first component, not retrofitted.
- **WCAG 2.1 AA**, checked in CI at two levels.
- shadcn/ui on **Radix** (`shadcn init -b radix`), deliberately not the Base UI default, because Tremor is Radix-based.
- TanStack Router, Query, Form and Table; lucide-react for icons.
- `GET /<slug>` is the hot path and belongs to the Go service. Nothing here touches it.
- Never store a full IP address.
- Vercel hosts `apps/web` and `apps/api` as two projects from one repository, each with its own Root Directory.
- Conventional Commits, checked in CI.
- oxlint and oxfmt, never ESLint or Prettier.

---

## The browser never calls the API

### The decision

Every call to `api.kurze.url` happens inside a TanStack Start server function. The browser talks only to its own origin. `createApiClient` from `packages/api-client` is constructed server-side, per request, with its base URL resolved by `withRelatedProject`.

### The problem this solves

The Go API has no CORS middleware, and adding one is not free. A browser calling `api.kurze.url` from `app.kurze.url` needs a preflight for every request that carries an `Authorization` header, and the allowlist has to admit Vercel's preview origins — which are minted per deployment. That leaves a choice between a suffix match broad enough to admit any `*.vercel.app` origin, or an environment variable carrying the frontend origin that has to be kept correct across two projects.

Keeping the browser on one origin removes the question rather than answering it. No CORS middleware is added to the Go service by this plan, and no backend change appears in a frontend plan.

### What it buys beyond CORS

The Supabase access token never has to exist in JavaScript. Plan 6 can put it in an httpOnly cookie the browser cannot read, which is a materially better posture than a token in memory or `localStorage`, and it is only available because of the boundary drawn here. This spec does not implement that — it declines to preclude it.

### What it costs

One Vercel function invocation per API call, against a free-tier budget. Accepted: the alternative costs a CORS surface and a token in the browser, and a Verein's dashboard traffic is not the shape that exhausts an invocation budget. Worth measuring once real usage exists rather than optimizing now.

Every data path becomes a server function rather than a direct fetch. TanStack Query still works — it calls server functions instead of the API — but the shape is worth knowing before plan 6 writes its first query.

### The rejected alternative

A Vercel rewrite proxying `/api/*` to the API origin also removes CORS and keeps the SDK in the browser. It was rejected because it still pays the edge hop, so it costs roughly what server functions cost while giving up the httpOnly cookie, and because it hides the real API origin in a way that makes debugging a preview-to-preview mismatch harder rather than easier.

---

## Where the language lives

### The decision

A cookie holds the viewer's language. It is read server-side during SSR, so the first paint is already in the right language. Routes carry no locale segment: `/links`, not `/en/links`.

### Why not a path prefix

A path prefix is the conventional answer, and it is the right one for a content site: the language is explicit, shareable and separately cacheable, and search engines index each locale separately.

Almost none of that applies here. Every page this application will ever grow is behind authentication and therefore not indexed; the one public page is a shell. What a prefix would add is a locale layer in the route tree and a locale on every internal link and redirect, paid on every route forever, to make a property observable that only one page can benefit from.

There is a genuine trade: a link shared between two people opens in the recipient's language rather than the sender's. For a Verein's internal tool — where the people sharing links are colleagues sending each other a page of their own dashboard — that is usually what is wanted, and where it is not, it is a mild surprise rather than a broken link.

If a public, indexable surface ever appears, this decision is worth reopening. The cookie does not prevent adding prefixed public routes later.

### Why not Accept-Language alone

`docs/planning/03-frontend.md` ships both languages deliberately, and inferring from the browser header with no persistence gives a viewer whose browser is set to English no way to choose German short of changing their browser. Accept-Language is used once, as the initial guess before a cookie exists.

---

## The theme uses the same mechanism

A cookie holds the choice, read at the same point during SSR. Tailwind's `class` dark-mode strategy, with the class applied to the document during server rendering rather than by a client effect.

This is the whole reason the theme is a cookie rather than `localStorage`: a value the server cannot read forces the first paint to guess, and a wrong guess is the flash of the wrong theme that every "add dark mode later" project ships. Doc 03 puts dark mode in MVP scope specifically so it is not retrofitted; reading it during SSR is what makes that mean something.

Colour tokens are defined once and consumed by both shadcn/ui and Tremor, which compose without a styling mismatch because both are Tailwind-based. Tremor is not installed by this plan, but the tokens are named so that it can be later without a second palette.

---

## Proving the deployment path

One server function calls `GET /v1/health` and surfaces the result unobtrusively.

It is here because it is the cheapest way to prove the part of the system with the most moving pieces and the least local observability: `withRelatedProject` resolving the right API URL, a preview of `apps/web` reaching the matching preview of `apps/api` rather than production, and a server function being able to reach the API at all from Vercel's runtime.

Plan 6 depends on every one of those and adds Supabase PKCE on top. Discovering that the Related Projects wiring is wrong while also debugging an authorization-code exchange is a bad afternoon; discovering it here, with an endpoint that needs no auth and returns `{"status":"ok"}`, is a good five minutes.

`/v1/health` is the only operation in the OpenAPI document that declares no security, which is what makes it usable before plan 6 exists.

---

## Accessibility, at two levels

Both levels ship here, rather than one being deferred to a plan with more surface to check.

- **Storybook's a11y addon** gives component-level feedback while a component is being written.
- **`@axe-core/playwright`** checks whole rendered pages, catching composition-level problems — focus order across a page, landmark structure — that no single component's story reveals.

`CLAUDE.md` states accessibility is "checked in CI at two levels". A plan that establishes one level has not established that rule; it has established half of it and left the other half to a future plan that will have more pages to retrofit, not fewer.

The E2E accessibility check runs against both themes and both languages. Contrast is a property of the theme, and German strings are reliably longer than their English equivalents — which is a common way for layouts to break, and doc 03 names it as something Storybook should surface.

---

## Enforcing "no hardcoded string"

The rule is unenforceable by intention. Someone will type a user-facing string during a refactor, and no reviewer catches all of them.

It cannot be enforced by the linter either: oxlint's configured rule set here is 529 rules and none of them is a JSX-literal or i18n rule — there is no equivalent of `react/jsx-no-literals` or `i18next/no-literal-string` available. Checking for one and finding nothing is why this section names a different mechanism rather than assuming the obvious one exists.

Two checks together, both behavioural, which is stronger than a lint rule would have been anyway — a linter sees JSX text nodes, while a hardcoded `aria-label` or a string assembled in a variable slips past it and still reaches a screen reader:

**Catalogue parity.** A test asserts the English and German catalogues have identical key sets. This catches the ordinary drift of adding a key to one file and forgetting the other, and it is cheap enough to run in the unit suite.

**Rendered-text divergence.** A Playwright check loads the page in English, loads it in German, and asserts that no rendered user-facing string is identical across the two. A hardcoded string is invisible to catalogue parity — it has no key at all — but it cannot help failing this one, because it does not change when the language does.

That check needs a small, explicit allowlist for strings legitimately identical in both languages: the product name, numerals, and any loanword that happens to match. The allowlist is the point of control — adding to it is a deliberate, reviewable act, where a hardcoded string is an accident.

Both language files are real from the first string. Neither is a copy of the other with a `TODO`, because a German file full of English text passes catalogue parity while failing the actual requirement — and is exactly what the divergence check exists to catch.

---

## Testing

Following the backend plans: real tests, no scaffolding that asserts nothing.

- **Vitest + React Testing Library** with at least one real component test — the language switcher is the natural candidate, since it exercises i18n and interaction together.
- **MSW** configured and exercised by at least one test, so the pattern exists before plan 6 needs it. It mocks the API the server functions call.
- **Playwright** asserting what this plan actually delivers: the page renders server-side, the theme survives a reload, axe reports no violations in both themes and both languages, and the rendered-text divergence check above holds. That last one is the language assertion — stated once, in its own section, rather than as a weaker "switching language changes some text" duplicate here.

Plan 6 will need all three harnesses immediately. Standing them up here, each with one honest test, is what makes them usable rather than aspirational.

E2E runs against the pull request's Vercel preview, per `docs/planning/07-repo-structure-and-tooling.md`, not on every push.

---

## Deployment

Per doc 07, unchanged by this spec: a second Vercel project with Root Directory `apps/web`, TanStack Start as a first-class framework, automatic build skipping via the pnpm workspace graph, and `apps/web/vercel.json` naming the API project as a related project so `withRelatedProject` resolves the matching preview.

`apps/web` qualifies for Vercel's automatic build skipping because it is inside the pnpm workspace — no Ignored Build Step is needed here, unlike `apps/api`.

---

## Open questions

None blocking. Two things this spec deliberately leaves to later plans:

- **Whether the invocation cost of routing every call through a server function matters.** It is a real cost against a real budget, and the honest answer needs traffic that does not exist yet. Worth measuring before it is optimized.
- **Whether a public, indexable surface ever appears.** If it does, the language-in-a-cookie decision is worth reopening for those routes specifically — the cookie does not prevent adding prefixed public routes alongside it.
