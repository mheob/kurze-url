# Vite+ migration and dependency refresh: design

Status: approved for planning once the maintainer signs off on this document. Date: 2026-10-17.

## Intent

The JavaScript and TypeScript half of this monorepo moves to **Vite+** (`vite-plus`, VoidZero's unified toolchain). The maintainer already runs Vite+ in other projects and wants this repository to match them. The motivation is simpler configuration and a better developer experience: one CLI (`vp`) and one place for configuration, instead of separate oxlint, oxfmt, Vite, Vitest and TypeScript setups that each have their own config file and script.

In the same effort, every outdated dependency is brought up to date by hand. Renovate's grouped PR #121 was closed instead of merged. After its last rebase it no longer built, because `@tanstack/react-start` 1.169.1 removed `getResponse`, and the maintainer asked for the updates to be checked independently rather than taken from that PR.

## Goals

1. **Vite+ is the toolchain** for every JS/TS package in the workspace: the root, `apps/web` and `packages/api-client`.
   - The local scripts and the CI workflows call `vp` commands.
   - Lint and format configuration lives in the `lint` and `fmt` blocks of a root `vite.config.ts`.
2. **Behaviour is preserved:**
   - the lint rule set and its severities;
   - the formatting output;
   - the type-check strictness;
   - both Vitest projects, `unit` (jsdom) and `storybook` (browser mode on Playwright);
   - the Storybook build;
   - the Playwright e2e suite;
   - the TanStack Start production build on Vercel.
3. **Every outdated dependency is current**, within the pnpm release-age gate. That covers JS (`pnpm outdated -r`) and Go (`apps/api`), with the exceptions listed under Constraints.
4. **The `@tanstack/react-start` 1.169 break is fixed properly.** The cookie-appending code in `apps/web/src/server/session.ts` and its tests move to whatever API replaces `getResponse`. The fix keeps the property its current comment insists on: several `set-cookie` values within one request are appended, never overwritten.

## Non-goals

- Changing any lint rule, formatting rule or TypeScript compiler option on purpose. A change the migration forces is reported, not slipped in. The two deliberate exceptions follow the templates and are named in the PR: Vite+'s own import rule (decision 11), and type-aware linting if it is not already active (decision 3).
- Replacing lefthook. The maintainer decided lefthook stays. `vp migrate` leaves it alone and only warns, and this design does not adopt Vite+'s `staged` or commit-hook feature.
- Migrating `github.com/piglig/go-qr` to v2. That was deferred on 2026-10-17 (Renovate PR #125 closed) and is a separate task.
- Moving `packages/api-client` off TypeScript 6. It is pinned there on purpose, because `@hey-api/openapi-ts` cannot run on TypeScript 7 (see CLAUDE.md).
- Adopting Vite Task caching (`vp run` with `cache` config), `vp pack`, or Vite+'s management of the Node.js runtime. Each can be considered later and none is needed for the goals.

## Anti-goals (outcomes that count as failure even if CI is green)

- A lint or format rule silently stops applying.
  - This has happened twice here already. The better-tailwindcss settings were dropped by `extends`, and every Tailwind rule was off until 2026-09-13.
  - Generated files drifted between oxfmt and oxlint.
  - A rule set that shrinks unnoticed is the failure most worth guarding against.
- Generated files get linted or formatted again. `generated.config.ts`'s list must still exclude them for both tools.
- `oxfmt` and `oxlint --fix` start undoing each other's Tailwind class order again. The `sortTailwindcss.stylesheet` and better-tailwindcss `entryPoint` must keep pointing at `apps/web/src/styles/app.css`.
- `routeTree.gen.ts` loses its trailing `declare module '@tanstack/react-start'` registration block. CLAUDE.md explains why that fails silently.
- A second copy of TanStack `router-core` under Storybook (CLAUDE.md's "one trap outlives them").
- The release-age gate is bypassed to get a newer version.

## Constraints

- **pnpm release-age gate:** since 2026-10-17 the repository uses pnpm's default, by the maintainer's decision (PR #131): one day, non-strict, with `@mheob/*` excluded. The gate is never bypassed with `--config.minimum-release-age=0` or similar. The target `vite-plus` version is the newest release the gate admits on the day of implementation. On 2026-10-17 that is 1.3.0, released 2026-10-16.
- **Prerequisites are met:**
  - Node `^22.18.0 || ^24.11.0 || >=26.0.0`: `.nvmrc` says `24`, and Vercel runs 24.x.
  - Vite 7+: the repo is on 8.3.
  - Vitest 4+: the repo is on 5.0.3, with `@vitest/browser-playwright` 5.0.3.
- **Migration procedure (from the Vite+ migration guide and its official migration prompt):**
  - Run the migrator from the workspace root with the target version explicit: `pnpm dlx --package=vite-plus@<target> vp migrate --no-interactive`.
  - Run it before upgrading Vite, Vitest or `vite-plus` by hand.
  - Resolve every BLOCK finding and re-run.
  - Review every REVIEW finding and manual-migration warning.
  - Keep any generated Vitest-v4 compatibility settings for the first validation run, then remove them where that needs no or only small changes.
  - Keep the vite and vitest entries the migrator configures for pnpm.
  - Keep `declare module 'vitest'` augmentations on their upstream identity.
- **One owner per concern.** Lint and format config moves to the root `vite.config.ts` `lint` and `fmt` blocks. Vite+ recommends against keeping `oxlint.config.ts` or `oxfmt.config.ts` next to them. `apps/web/vite.config.ts` keeps the app, plugin and Vitest configuration. Package-level `lint` and `fmt` blocks are not used; package-specific rules go into `lint.overrides`.
- **The shared configs stay the source of the rule set.** `@mheob/oxlint-config` (`baseJsConfig`, `reactConfig`, `storybookConfig`, `tailwindcssConfig`) and `@mheob/oxfmt-config` (`baseConfig`) are the maintainer's own packages. The root `vite.config.ts` composes them the way `oxlint.config.ts` and `oxfmt.config.ts` do now, keeping every repo-specific addition:
  - the top-level better-tailwindcss `settings`;
  - `ignoredClasses`;
  - `generatedFiles` as `ignorePatterns`;
  - `sortTailwindcss`;
  - every override. If the shared packages turn out to be unusable from a `vite-plus` config as they stand, the fix belongs in those packages. That is a finding to bring back to the maintainer, not something to work around inside this repo.
- **Commits:**
  - Conventional Commits, subject at most 50 characters, no co-author lines.
  - All git writes through GitButler (`but`).
  - `pnpm format` (or its `vp` successor) before every commit.
  - `apps/web/src/routeTree.gen.ts` is included only when it actually changed.

## Decisions taken by the maintainer

- Move to Vite+, following the official migration guide and its migration prompt.
- Do the dependency updates by hand and check them independently. Renovate's #121 is closed.
- Keep lefthook.
- Keep `go-qr` on v1 for now.
- **When Vite+ cannot preserve something 1:1, the builder stops and reports it, and the maintainer and the builder decide together.** There is no partial hybrid that keeps one concern on the old tooling, and no workaround chosen alone. This covers the shared `@mheob` configs too.
- **Templates:** the maintainer's projects that already run Vite+ are the reference for every choice this document leaves open.
  - `mheob/config` (locally `~/dev/tools/config`), the monorepo that publishes `@mheob/oxlint-config` and `@mheob/oxfmt-config`. It shows the minimal root `vite.config.ts`.
  - A second, private project of the maintainer's, which shows a larger application config with overrides, lefthook and editor settings. Its name and path stay out of this public repository.

## What the templates settle (observed 2026-10-17, both on `vite-plus` 1.1.0)

- **Config shape.** One root `vite.config.ts` imports `defineConfig` from `vite-plus`.
  - `fmt` spreads `@mheob/oxfmt-config`'s `baseConfig`, then adds repo-specific keys such as `ignorePatterns`.
  - `lint` uses `extends: [<shared config>]`, then `rules` and `overrides`.
- **Vite+'s own lint plugin.** Both templates set `jsPlugins: [{ name: 'vite-plus', specifier: 'vite-plus/oxlint-plugin' }]` and `'vite-plus/prefer-vite-plus-imports': 'error'`. So test files import from `vite-plus/test`, not `vitest`; the private template has 186 such imports and none from `vitest`. kurze-url has 91 `from 'vitest'` imports today.
- **Lint options.** Both templates set `options: { typeAware: true, typeCheck: true }` at the top level of `lint`. That is also what `@mheob/oxlint-config`'s `baseConfig` declares.
- **pnpm wiring** (what `vp migrate` sets up):
  - `vite` resolves to `npm:@voidzero-dev/vite-plus-core@<version>`, with `vite-plus` pinned to the same exact version;
  - `overrides` force `vite@*` (and `vitest@*` where Vitest is used) onto those entries;
  - `peerDependencyRules.allowAny: [vite]`, because Vite+ core is 1.x and outside the `vite` ranges other packages ask for.
  - Both templates keep these in a pnpm catalog. kurze-url has no catalog, so whether to introduce one is left to the builder.
- **Scripts.** `format` is `vp fmt`, `format:check` is `vp fmt --check`, `lint` is `vp lint`, `lint:fix` is `vp lint --fix`, and `typecheck` stays `tsc --noEmit`.
- **Lefthook** stays, with staged-file jobs `pnpm exec vp fmt --check --no-error-on-unmatched-pattern {staged_files}` and `pnpm exec vp lint --no-error-on-unmatched-pattern {staged_files}`, and `tsc --noEmit` in pre-push.
- **Zed.** The `oxfmt` language server gets `"fmt.configPath": "./vite.config.ts"` in its `initialization_options.settings`, so format-on-save reads the `fmt` block.

## Decisions taken by the author of this document (open to the maintainer's veto)

1. **Order:**
   1. the Vite+ migration;
   2. the JS dependency updates, with the TanStack fix;
   3. the Go dependency updates. Vite and Vitest come with `vite-plus`, so updating them first would be wasted work, and the guide warns that it can stop the migrator from recognising the old runner.
2. **Delivery:** three pull requests in that order, each green on its own. If the migration changes no dependency versions beyond what Vite+ requires, the JS updates may ride in the same PR as separate commits. The Go updates stay separate, because they touch a different module and toolchain.
3. **Type checking keeps its current authority, and lint follows the templates.**
   - `tsc --noEmit` against the root `tsconfig.json` stays the type check: the `typecheck` script, CI and the pre-push hook. It covers files that lint skips: `routeTree.gen.ts`, `components/ui/**` and the generated API client. A type check through oxlint alone would silently stop covering them. `apps/web/tsconfig.json` is not made authoritative.
   - The `lint` block sets `options: { typeAware: true, typeCheck: true }` at its top level, as both templates do and as `@mheob/oxlint-config` declares. Type errors then also show up in `vp lint`, so CI checks types twice. The private template accepts the same cost.
   - Whether today's `oxlint.config.ts` actually resolves these options is unverified. oxlint drops `settings` from an `extends` entry (see the Tailwind comment in `oxlint.config.ts`), and it may drop `options` the same way. The builder checks this first. If they are not active today, switching them on is a deliberate change: the PR names it, and the findings it brings follow decision 7.
4. **Scripts keep their names and their split.**
   - `format` runs `vp fmt`, `format:check` runs `vp fmt --check`, `lint` runs `vp lint`, `lint:fix` runs `vp lint --fix`, and `typecheck` stays `tsc --noEmit`. The exact flags follow the Vite+ CLI docs.
   - A `check` script running `vp check` may be added for local use. CI and the hooks keep the split scripts, so nothing runs twice.
   - `apps/web`'s `build`, `dev` and `test*` scripts call `vp build`, `vp dev` and `vp test`. `build-storybook` and `storybook` stay on the `storybook` CLI.
5. **CI and required checks:** `.github/workflows/ci-js.yml` keeps its job names, because the `main` ruleset requires `format, lint & typecheck`, `build & test Storybook`, `e2e against the preview` and the Vercel checks by name. The jobs call the same root scripts as today. They need no new secrets.
6. **TanStack `getResponse` replacement:** use the public API `@tanstack/react-start/server` (or `@tanstack/start-server-core`) offers in the target version for appending a header to the current response without replacing existing `set-cookie` values. If no such API exists, report that before choosing a workaround.

7. **New findings from the bundled tool versions.** `vite-plus` pins exact versions of oxlint, oxfmt, oxlint-tsgolint and Vitest, which are newer than the repo's. Because `@mheob/oxlint-config` enables whole rule categories, new rules can switch on with the bump.
   - The before-and-after rule-set comparison runs at the same tool version: the old `oxlint.config.ts` against the new `lint` block, both on the bundled oxlint. That proves the config migration is lossless, separately from the version bump.
   - Findings the version bump brings are fixed in the migration PR when the fix is mechanical. Otherwise they are listed in the PR for the maintainer.
   - A finding is never silenced with a new override or a lowered severity without being named in the PR.
   - `@mheob/oxlint-config` 4.1.1, whose peers already expect oxlint 1.87 or later, is updated in the migration PR rather than the JS-update PR.
8. **Delivery mechanics:**
   - The PRs are sequential. The next one starts from `main` once the previous one has merged.
   - The migration PR opens as a draft and is marked ready only once every job is green: the required checks, plus `web unit tests` (not required) and the e2e run against the preview, which is the only check of the production Nitro build.
   - Kodiak then merges it.
9. **Vercel's own build settings.** `apps/web/vercel.json` sets only the framework. The builder checks the `kurze-url-web` project's Build and Install Command settings, through Vercel's API or the dashboard. If either calls `vite` directly, changing it becomes a maintainer step in the plan, done before the migration PR merges.
10. **Hooks and editor follow the config.**
    - Lefthook stays the hook runner. Its pre-commit jobs, which call `pnpm exec oxfmt --check` and `pnpm exec oxlint` on staged files, move to the templates' `vp` commands, `--no-error-on-unmatched-pattern` included, so they read the `lint` and `fmt` blocks. The existing globs and the comment about `but commit` bypassing hooks stay.
    - `.zed/settings.json` gets the templates' `"fmt.configPath": "./vite.config.ts"` for the `oxfmt` language server. Format-on-save must not fall back to oxfmt's defaults once `oxfmt.config.ts` is gone.
11. **Vite+'s lint plugin is adopted.** The `lint` block registers `vite-plus/oxlint-plugin` and sets `'vite-plus/prefer-vite-plus-imports': 'error'`, as both templates do. Every `from 'vitest'` and `from 'vite'` import moves to its `vite-plus` equivalent in the migration PR. `declare module 'vitest'` augmentations keep their upstream identity, as the migration prompt says. This is a new rule, so the PR names it.

## Left to the builder

- The exact composition of the shared `@mheob` configs inside the `lint` and `fmt` blocks.
- How the migrator's REVIEW findings are resolved.
- The per-package `vite.config.ts` layout beyond the constraints above.
- Whether the Vitest workspace projects stay in `apps/web/vitest.config.ts` or move into `apps/web/vite.config.ts`'s `test` block. Follow the Vite+ docs, and keep the `unit` and `storybook` project names, because CI and the scripts filter by them.
- How `oxlint-tsgolint`, `oxlint`, `oxfmt`, `vite` and `vitest` direct dependencies are reduced, as far as Vite+ supplies them.

## Updates in scope (as found on 2026-10-17)

The JS updates (`pnpm outdated -r`) are all patch or minor. The ones that need attention:

| Package | Change | Note |
| --- | --- | --- |
| `@tanstack/react-start` | 1.168.60 → 1.169.1 | removes `getResponse`; fix in Goal 4; watch for a second `router-core` under Storybook |
| `@tanstack/react-router` | 1.170.41 → 1.170.44 | moves with the TanStack family |
| `@sentry/tanstackstart-react` | 11.1 → 11.7 | a new `dataCollection` field fails `satisfies ExplicitDataCollection` on purpose; set it to the off-state value |
| `lucide-react` | 1.49 → 1.57 | check for renamed icons |
| `oxfmt` / `oxlint` / `oxlint-tsgolint` | 0.71 → 0.73 / 1.86 → 1.88 / 7.0.2004 | likely supplied by Vite+; oxfmt 0.73 reformats five planning documents |
| `vite` / `vitest` / `@vitest/browser*` | 8.3.1 → 8.3.4 / 5.0.3 → 5.0.4 | supplied by Vite+ |

The rest are low-risk patch and minor updates:

- Storybook 10.6.2;
- Playwright 1.64;
- `@base-ui/react` 1.9, `@supabase/supabase-js`, `@tanstack/react-query`;
- `i18next`, `react-i18next`, `msw`, `shadcn`, `jsdom`, `axe-core`;
- `lefthook` 2.2, `cve-lite-cli`;
- the eslint plugins and `@mheob/oxlint-config` 4.1.1;
- `@types/node`, `@vitejs/plugin-react`, `@testing-library/user-event`.

TypeScript in `packages/api-client` stays on 6.

The Go updates (`apps/api`) are all minor or patch:

- `sentry-go` 0.49 → 0.50: check the test fake transport against the `Transport` interface, and check `Flush` versus `FlushWithContext` routing.
- `go-chi/chi/v5` 5.3.2 → 5.5.0.
- `go-redis/v9` 9.22 → 9.23.
- `x/crypto`, `x/net`, `x/sync`.
- `protobuf` 1.36.12.

The builder re-runs both inventories on the day of implementation instead of trusting this list.

## Verification

Each of these runs from the root, and all must pass with the same outcomes as before. No test is weakened or skipped to get there.

- `vp check`, or the scripts that call it.
- `vp test` for the `unit` project and the `storybook` browser project. This needs Playwright Chromium.
- `pnpm --filter @kurze-url/web build`, and the Storybook build.
- The Playwright e2e suite against a Vercel preview, run by CI.
- `go test ./...` and `golangci-lint` for `apps/api` after the Go updates.

Some checks are specific to this migration:

- **No rules lost:** the resolved lint rule count and severities match before and after, apart from the additions decisions 3 and 11 name. The repo has precedent for comparing resolved rule sets; `oxlint.config.ts` cites 529 rules. The eight better-tailwindcss rules are active: there is no "Tailwind CSS is not installed" warning, and a deliberately unknown class is reported.
- **No new formatting:** formatting the whole repo with the migrated config produces no diff beyond what oxfmt 0.73 changes on its own. Those changes are reviewed and committed separately.
- **Generated files stay excluded:** they are neither linted nor formatted, and `vite build` leaves the working tree clean.
- **Route registration intact:** `routeTree.gen.ts` still ends with the `Register` block after a build.
- **One router core:** `pnpm why @tanstack/router-core` shows a single version.
- **Lefthook still works:** the hooks run `vp`-based scripts on a test commit.

## Documentation

CLAUDE.md names `oxlint.config.ts`, `oxfmt.config.ts`, `generated.config.ts`, `vite build` and `tsc --noEmit` in several entries. Each mention is updated to the new layout in the same PR that changes it:

- the shadcn generated-files rule;
- the `routeTree.gen.ts` entry;
- the Sentry source-map entry, if the Vite plugin order changes;
- the Renovate entry's `pnpm update` advice;
- the TypeScript codegen entry.

The memory note `js-ts-lint-format-tooling.md`, "oxlint + oxfmt with the @mheob shared configs", is updated to say Vite+.
