# Vite+ Migration and Dependency Refresh Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every JS/TS package in the workspace runs on Vite+ (`vp`), with lint, format, test and build behaviour unchanged. Every outdated JS and Go dependency is current, and the `@tanstack/react-start` 1.169 break is fixed.

**Architecture:** There are three pull requests, opened one after another, each starting from `main` once the previous one has merged.

1. **Migration (Tasks 1–4):** runs the official migrator and hand-ports `oxlint.config.ts` and `oxfmt.config.ts` into the `lint` and `fmt` blocks of a new root `vite.config.ts`. It moves the test imports to `vite-plus`, routes the scripts through `vp`, and points lefthook, Zed, CI and CLAUDE.md at the new layout.
2. **JS updates (Tasks 5–6):** replaces the removed `getResponse` with `appendResponseHeader` and updates every JS dependency.
3. **Go updates (Task 7):** updates `apps/api`'s modules.

**Tech Stack:** `vite-plus` 1.3.0, which bundles:

- vite core 8.3.4;
- vitest 5.0.5;
- oxlint 1.88.0, oxfmt 0.73.0 and oxlint-tsgolint 7.0.2004.

Alongside it: pnpm 12.13, TanStack Start on Nitro, Storybook 10 with `@storybook/addon-vitest`, Playwright, lefthook, and Go 1.27 for `apps/api`.

**Spec:** `docs/superpowers/specs/2026-10-17-vite-plus-migration-design.md`. Read it before any task. It is the authority wherever this plan and it disagree.

## Global Constraints

Copied from the spec. Every task's requirements include all of them.

- **Stop and report.** When Vite+ cannot preserve something 1:1, stop and report it; the maintainer and the builder decide together. There is no partial hybrid that keeps one concern on the old tooling, and no workaround chosen alone. This covers the shared `@mheob` configs too.
- **Release-age gate.** It is never bypassed with `--config.minimum-release-age=0` or similar. The target `vite-plus` version is the newest release the gate admits on the day of implementation; on 2026-10-17 that is 1.3.0.
- **Migrator procedure.**
  - Run it from the workspace root, before upgrading Vite, Vitest or `vite-plus` by hand.
  - Resolve every BLOCK finding and re-run.
  - Review every REVIEW finding and manual-migration warning.
  - Keep the `vite` and `vitest` entries the migrator configures for pnpm.
- **One owner per concern.**
  - Lint and format config lives only in the root `vite.config.ts` `lint` and `fmt` blocks. Package-specific rules go into `lint.overrides`.
  - `oxlint.config.ts` and `oxfmt.config.ts` are deleted.
  - `apps/web/vite.config.ts` keeps the app, plugin and Vitest configuration.
  - No package-level `lint` or `fmt` blocks.
- **The shared configs stay the source of the rule set.** The root config composes `@mheob/oxlint-config` (`baseJsConfig`, `reactConfig`, `storybookConfig`, `tailwindcssConfig`) and `@mheob/oxfmt-config` (`baseConfig`). It keeps:
  - the top-level better-tailwindcss `settings`;
  - `ignoredClasses`;
  - `generatedFiles` as `ignorePatterns`, for both tools;
  - `sortTailwindcss`, pointing at `apps/web/src/styles/app.css`;
  - every override.
- **No rule, formatting or compiler option changes on purpose.** The only deliberate addition is Vite+'s `vite-plus/prefer-vite-plus-imports` (spec decision 11). A finding is never silenced with a new override or a lowered severity unless the PR names it.
- **Out of scope:**
  - `packages/api-client` stays on TypeScript 6.
  - `github.com/piglig/go-qr` stays on v1.
  - Lefthook stays the hook runner. Vite+'s `staged` and commit-hook features are not adopted.
- **Type checking.** `tsc --noEmit` against the root `tsconfig.json` stays the type-check authority: the `typecheck` script, CI and pre-push.
- **CI.** Job names in `.github/workflows/ci-js.yml` are unchanged. The `main` ruleset requires `format, lint & typecheck`, `build & test Storybook` and `e2e against the preview` by name.
- **Commits.**
  - Conventional Commits, subject at most 50 characters, no co-author lines.
  - Every git write goes through GitButler (`but`). `but commit` bypasses hooks, so run `pnpm format` yourself before each commit, except where a step says otherwise.
  - `apps/web/src/routeTree.gen.ts` is committed only when it actually changed.
  - Implementers never push and never touch a pull request; the controller does.
- **Baseline artifacts.** Write them to `.superpowers/sdd/2026-10-17-vite-plus-migration/baseline/`, which is git-ignored, and which `baseline/` below refers to. Never commit them.

## Rulings on the spec

Research on 2026-10-17 settled these. Each sits within what the spec leaves to the builder.

1. **`vp migrate` 1.3.0 does not detect TypeScript config files.** It recognises only `.oxlintrc.json(c)` and `.oxfmtrc.json(c)` (voidzero-dev/vite-plus#2430; fix #2483 is unmerged). It may therefore:
   - drop `oxlint`, `oxfmt` and `oxlint-tsgolint` from the devDependencies;
   - write `fmt: {}`.

   Both configs are hand-ported in Task 1. This is not a stop condition: the spec leaves the composition to the builder, and a hand-port preserves behaviour.

2. **Type-aware linting is already on.** `@mheob/oxlint-config` declares `options: { typeAware: true, typeCheck: true }`, and oxlint merges `options` field by field through `extends`; `--print-config` on today's config shows both. Setting them explicitly at the top of the `lint` block, as the templates do, changes nothing. Spec decision 3's "if not active" branch does not apply.
3. **`--print-config` does not show rules from JS plugins.** That covers `better-tailwindcss/*`, `regexp/*` and `storybook/*`. The rule-set comparison therefore uses `--print-config` for the native rules, and a set of probe files for the JS-plugin rules (Task 1, Step 8).
4. **`getResponse` has a public replacement.** It was removed in `@tanstack/start-server-core` 1.169.41 (TanStack/router#7588), which `@tanstack/react-start` 1.169.0 pulls in.
   - The replacement is `appendResponseHeader(name: ResponseHeaderName, value: string | Array<string>): void` from `@tanstack/react-start/server`.
   - It merges `set-cookie` values. Two values with the same name, domain and path within one request collapse to the last one, which is what a browser does when it applies them in order. Every distinct cookie is kept, so spec Goal 4's "appended, never overwritten" holds.
   - `setResponseHeader('set-cookie', …)` replaces every cookie on the response and must not be used.
5. **Migrator flags.** Run it with `--no-interactive --no-hooks --no-agent --no-editor`, so it touches neither lefthook, CLAUDE.md nor the editor settings; Task 4 does those by hand. If it rewrites the root `prepare` script to `vp config`, restore the lefthook command.
6. **`@vitest/browser-playwright` must equal the bundled Vitest version.** `vite-plus@1.3.0` peers it at exactly `5.0.5`. The migrator realigns it and removes the direct `@vitest/browser` entry, which nothing imports.
7. **The Vitest projects stay in `apps/web/vitest.config.ts`.** Vitest 5 looks for a config in its working directory only, and the `unit` and `storybook` names stay as they are.
8. **`@tanstack/nitro-v2-vite-plugin` is deprecated upstream.** TanStack/router#8537 removed it; its last version is 1.155.2, which the repo already uses. Moving off it is out of scope; report it as a follow-up.
9. **Two migration-prompt rules have nothing to act on.** No `declare module 'vitest'` augmentation exists in the repo. And the repo is already on Vitest 5, so the migrator adds no Vitest-v4 compatibility settings to remove.
10. **Spec decision 9 needs no maintainer step.** The `kurze-url-web` Vercel project runs `pnpm run build`, as its build log shows (checked 2026-10-17). So once the `build` script calls `vp build`, Vercel follows it.

## Review Focus

These are the failure modes most likely to bite, most likely first. Each one's check sits in the task named.

1. **A commit that stages only generated or ignored files**, such as a regenerated `routeTree.gen.ts` or a `components/ui/` file. The pre-commit hook must pass instead of failing on an unmatched pattern. (Task 4)
2. **`vp test` typed at the repository root.** It must run the web suites the way `pnpm test` does, or refuse clearly. Running the web tests silently under the root config, without jsdom and the setup file, is not acceptable. (Task 3)
3. **Local development.**
   - `pnpm --filter @kurze-url/web dev` must serve the app on port 3000.
   - `pnpm --filter @kurze-url/web storybook` must start.

   CI exercises neither. (Task 3)

4. **A production build must publish no more source maps than before.** CLAUDE.md requires uploaded maps to be deleted, and a changed build path could leave them in `.output/public`. (Task 3, with its baseline in Task 1)
5. **A test file added later that still imports from `vitest`** must fail `vp lint`, not pass. (Task 2)

---

## Pull request 1: the Vite+ migration

The controller creates the branch `build/vite-plus` from `main` before Task 1: `but branch new build/vite-plus`.

### Task 1: Migrate and port the lint and format configs

**Files:**

- Create: `vite.config.ts` (repository root)
- Delete: `oxlint.config.ts`, `oxfmt.config.ts`
- Modify:
  - `package.json`, `apps/web/package.json`, `packages/api-client/package.json`, as the migrator decides;
  - `pnpm-workspace.yaml`;
  - `pnpm-lock.yaml`.
- Keep: `generated.config.ts`, unchanged and imported by the new `vite.config.ts`.

**Interfaces:**

- Produces:
  - A root `vite.config.ts` whose default export is `defineConfig({ fmt: {...}, lint: {...} })` from `vite-plus`. Tasks 2 and 4 edit its `lint` block.
  - The baseline files listed in Step 2, which Tasks 2 and 3 compare against.

- [ ] **Step 1: Pick the target version.** Run `npm view vite-plus time --json`. Take the newest version published more than 24 hours ago, and record it as `<target>` in your report. On 2026-10-17 that is 1.3.0.
- [ ] **Step 2: Record the baseline** in `.superpowers/sdd/2026-10-17-vite-plus-migration/baseline/`, on the unmigrated tree:

  | Command | Saved as |
  | --- | --- |
  | `pnpm exec oxlint --print-config` | `rules-1.86.json` |
  | `pnpm format:check && pnpm lint && pnpm typecheck` | exit codes, all 0 |
  | `pnpm --filter @kurze-url/web test` | the "Tests … passed" line, in `unit.txt` |
  | `pnpm --filter @kurze-url/web exec playwright install chromium`, then `pnpm --filter @kurze-url/web test:storybook` | its "Tests" line, in `storybook.txt` |
  | `pnpm --filter @kurze-url/web build`, then `find apps/web/.output/public -name '*.map' \| wc -l` | `maps.txt` |
  | `tail -n 12 apps/web/src/routeTree.gen.ts` | `routetree-tail.txt` (must contain `interface Register`) |
  | `pnpm why @tanstack/router-core` | `router-core.txt` |
  | `pnpm outdated -r --format json` | `outdated-js.json` |
  | `cd apps/api && go list -m -u all \| grep '\['` | `outdated-go.txt` |

  Afterwards, `git status --porcelain` must be empty.

- [ ] **Step 3: Run the migrator** from the repository root, saving everything it prints to `baseline/migrate-output.txt`:

  ```bash
  pnpm dlx --package=vite-plus@<target> vp migrate --no-interactive --no-hooks --no-agent --no-editor
  ```

  Resolve each BLOCK finding and run the migrator again until there are none. In your report, list every REVIEW finding and manual-migration warning together with what you did about it.

- [ ] **Step 4: Check the migrator's pnpm wiring** against the templates:
  - in a catalog: `vite: npm:@voidzero-dev/vite-plus-core@<target>`, `vitest` at the bundled version, and `vite-plus: <target>`;
  - `overrides` for `vite@*` and `vitest@*` pointing at those entries;
  - `peerDependencyRules.allowAny` covering `vite` (and `vitest`);
  - `@vitest/browser` removed from `apps/web`, and `@vitest/browser-playwright` at the bundled Vitest version.

  Also check:
  - `prepare` is still the lefthook command (Ruling 5);
  - `lefthook.yml`, `CLAUDE.md` and `.zed/` are untouched, and no `AGENTS.md` was created.

  Revert anything outside these rules.

- [ ] **Step 5: Update the shared configs.** Run `pnpm add -D -w @mheob/oxlint-config@^4.1.1`. Also update `@mheob/oxfmt-config` if `outdated-js.json` lists a newer version. Both are config sources, so they belong in this PR.
- [ ] **Step 6: Capture the old rule set at the bundled version** before deleting anything:

  ```bash
  pnpm exec vp lint -c oxlint.config.ts --print-config > .superpowers/sdd/2026-10-17-vite-plus-migration/baseline/rules-old-bundled.json
  ```

  This needs `oxlint` resolvable from the root, because `oxlint.config.ts` imports it. If the migrator dropped it, re-add it as a devDependency pinned exactly to the bundled oxlint version for this step.

- [ ] **Step 7: Write the root `vite.config.ts`**, following the private template's shape (spec, "What the templates settle"):
  - **`fmt`:** `...baseConfig` from `@mheob/oxfmt-config`, then:
    - `ignorePatterns: generatedFiles`;
    - `sortTailwindcss: { functions: ['cn', 'cva'], stylesheet: 'apps/web/src/styles/app.css' }`.
  - **`lint`, in this order:**
    - `extends: [baseJsConfig, reactConfig, storybookConfig, tailwindcssConfig({ ignoredClasses, options: tailwindPluginOptions })]`;
    - `ignorePatterns: generatedFiles`;
    - `options: { typeAware: true, typeCheck: true }` (Ruling 2);
    - the seven `overrides`, copied verbatim with their comments;
    - the top-level `rules` entry `'better-tailwindcss/enforce-consistent-line-wrapping': 'off'`;
    - `settings: { 'better-tailwindcss': tailwindPluginOptions }`.
  - **Constants:** `tailwindPluginOptions = { cwd: 'apps/web', entryPoint: 'src/styles/app.css' }` and `ignoredClasses = ['^dark$']`. Move their long comments from `oxlint.config.ts` with them; they explain why `settings` must be top level.
  - **Leave out for now:** the `vite-plus` jsPlugin and its rule. They come in Task 2.

  Then delete `oxlint.config.ts` and `oxfmt.config.ts`. Remove `oxlint`, `oxfmt` and `oxlint-tsgolint` from the root devDependencies, unless `pnpm install` then reports a missing peer for `@mheob/oxlint-config` or `@mheob/oxfmt-config`. In that case keep each one pinned exactly to the version `vite-plus` bundles, never another version.

- [ ] **Step 8: Prove the port is lossless at the same tool version.**
  - **Native rules.** Run `pnpm exec vp lint --print-config > baseline/rules-new-bundled.json`. Then compare `jq -S '.rules'` and `jq -S '.overrides'` of `rules-old-bundled.json` and `rules-new-bundled.json`. Expected: identical, with `.options` showing `typeAware: true` and `typeCheck: true` in both. Any difference is a stop-and-report.
  - **JS-plugin rules.** Create these three probe files, run `pnpm exec vp lint <the three files>`, then delete the files:

    | Probe file | Content | Must report |
    | --- | --- | --- |
    | `apps/web/src/lint-probe.tsx` | a component rendering `<div className="not-a-tailwind-class" />` | `better-tailwindcss/no-unknown-classes` |
    | `apps/web/src/lint-probe-regexp.ts` | `export const digits = /[0-9]/u;` | `regexp/prefer-d` |
    | `apps/web/src/lint-probe.stories.tsx` | a stories file with no default export | `storybook/default-exports` |

    The output must not contain "Tailwind CSS is not installed".
- [ ] **Step 9: Check formatting.** Run `pnpm exec vp fmt --check` and list the files it reports.
  - Expected: only files whose formatting oxfmt 0.73 changed. The spec names five `docs/superpowers/plans/*.md` files.
  - A reported file outside `docs/` that the formatter version does not explain points to a config-port error: fix the port, not the file.
  - Confirm that no file matching `generatedFiles` is reported.
- [ ] **Step 10: Record the new-version lint findings.** Run `pnpm exec vp lint > baseline/lint-findings-bundled.txt` and write the count in your report. Task 2 fixes them; do not fix them here.
- [ ] **Step 11: Commit**, without running `pnpm format` first, so the formatter-version changes stay out of this commit: `but commit` with subject `build: move lint and format to vite+`. It contains the config, `package.json` and lockfile changes, and nothing under `docs/`.
- [ ] **Step 12: Apply the formatter-version changes.** Run `pnpm exec vp fmt`, check that `git status` shows only the files Step 9 listed, then commit them as `style: apply oxfmt 0.73 formatting`.

### Task 2: Adopt the Vite+ imports and fix the new-version findings

**Files:**

- Modify:
  - `vite.config.ts` (root `lint` block);
  - the 91 test files importing `vitest`;
  - `apps/web/vitest.config.ts`, `apps/web/vite.config.ts`;
  - the files named in `baseline/lint-findings-bundled.txt`.

**Interfaces:**

- Consumes: the root `vite.config.ts` from Task 1, and `baseline/lint-findings-bundled.txt` and `baseline/unit.txt`.
- Produces: a tree where `vp lint` exits 0, and test files import from `vite-plus/test`.

- [ ] **Step 1: Write the failing check (Review Focus 5).** In the `lint` block, add:
  - `jsPlugins: [{ name: 'vite-plus', specifier: 'vite-plus/oxlint-plugin' }]`;
  - the rule `'vite-plus/prefer-vite-plus-imports': 'error'`.

  Then run `pnpm exec vp lint apps/web/src/lib/observability.test.ts`. Expected: FAIL with `vite-plus/prefer-vite-plus-imports` on its `from 'vitest'` import.

- [ ] **Step 2: Move the imports.** Use `pnpm exec vp lint --fix` if the rule offers a fix; otherwise rewrite them mechanically:

  | From                                         | To                                    |
  | -------------------------------------------- | ------------------------------------- |
  | `'vitest'`                                   | `'vite-plus/test'`                    |
  | `'vitest/config'`                            | `'vite-plus'`                         |
  | `'@vitest/browser-playwright'`               | `'vite-plus/test/browser-playwright'` |
  | `'vite'` (in `apps/web/vite.config.ts` only) | `'vite-plus'`                         |

  Leave these alone: `@testing-library/jest-dom/vitest`, `@storybook/addon-vitest/vitest-plugin`, `/// <reference types="vite/client" />`, and `"types": ["vite/client"]`.

- [ ] **Step 3: Fix the new-version findings** from `baseline/lint-findings-bundled.txt`. Mechanical fixes go in.
  - A finding whose fix would change behaviour or needs a judgement call is not silenced. List it with its file:line in your report, for the PR description.
  - If such a finding blocks `vp lint` from passing, stop and report.
- [ ] **Step 4: Verify.** Each of these must pass:

  | Command | Expected |
  | --- | --- |
  | `pnpm exec vp lint` | exit 0 |
  | `pnpm typecheck` | exit 0 |
  | `pnpm --filter @kurze-url/web test` | the same passed count as `baseline/unit.txt`, which proves `vi.mock` hoisting still works with `vite-plus/test` |
  | `grep -rn "from 'vitest'" apps packages --include='*.ts' --include='*.tsx'` | no output |
  | `pnpm exec vp lint --fix && pnpm exec vp fmt --check` | exit 0, and `git status --porcelain` shows nothing beyond this task's own edits; oxfmt and `lint --fix` do not undo each other's Tailwind class order (spec anti-goal) |

- [ ] **Step 5: Commit** in two commits, `pnpm format` first:
  1. `refactor: import test apis from vite-plus`: the plugin, the rule and the import moves.
  2. `fix: address oxlint 1.88 findings`: the fixes.

### Task 3: Run the app scripts through `vp`

**Files:**

- Modify: root `package.json` scripts, `apps/web/package.json` scripts, `apps/web/vitest.config.ts` (only if Step 3 needs it), root `vite.config.ts` (only if Step 4 needs it).

**Interfaces:**

- Consumes: Task 2's tree, and `baseline/unit.txt`, `storybook.txt`, `maps.txt`, `routetree-tail.txt` and `router-core.txt`.
- Produces: these scripts, which Task 4's hooks and CI call:

  | Package | Script | Command |
  | --- | --- | --- |
  | root | `format` | `vp fmt` |
  | root | `format:check` | `vp fmt --check` |
  | root | `lint` | `vp lint` |
  | root | `lint:fix` | `vp lint --fix` |
  | root | `check` (new) | `vp check` |
  | root | `typecheck` | `tsc --noEmit` (unchanged) |
  | root | `prepare` | the lefthook command (unchanged) |
  | `apps/web` | `build` | `vp build` |
  | `apps/web` | `dev` | `vp dev --port 3000` |
  | `apps/web` | `test` | `vp test --project unit` |
  | `apps/web` | `test:storybook` | `vp test --project storybook` |
  | `apps/web` | `test:watch` | `vp test watch --project unit` |
  | `apps/web` | `storybook`, `build-storybook`, `preview`, `start` | unchanged |

  `vp test` runs once and does not watch; `vp test watch` watches. Check both against `vp test --help`. If the CLI differs, use the documented form and say so in your report.

- [ ] **Step 1: Set the scripts** as in the table, keeping anything the migrator already wrote that matches.
- [ ] **Step 2: Verify the tests.**
  - `pnpm --filter @kurze-url/web test` must give the same passed count as `baseline/unit.txt`.
  - `pnpm --filter @kurze-url/web test:storybook` must give the same count as `baseline/storybook.txt`.
  - If `--project` is not passed through to Vitest, the counts show it: stop and report.
- [ ] **Step 3: Verify the builds.**
  - Run `pnpm --filter @kurze-url/web build`. Expected:
    - exit 0;
    - `tail -n 12 apps/web/src/routeTree.gen.ts` matches `baseline/routetree-tail.txt`;
    - `git status --porcelain` is empty;
    - `find apps/web/.output/public -name '*.map' | wc -l` equals `baseline/maps.txt` (Review Focus 4).
  - Run `pnpm --filter @kurze-url/web build-storybook`: exit 0.
  - Run `pnpm why @tanstack/router-core`: a single version, as in `baseline/router-core.txt`.
- [ ] **Step 4: Check `vp test` at the root (Review Focus 2).** Run `pnpm exec vp test` from the repository root.
  - Acceptable: it runs the `unit` and `storybook` projects with the counts above, or it exits non-zero with a message saying there is nothing to run there.
  - Not acceptable: it runs `apps/web` tests without jsdom or `src/test/setup.ts`, which shows as failures like `document is not defined`. In that case, add `test: { projects: ['apps/web'] }` to the root `vite.config.ts` and run it again. If that still does not give the counts above, stop and report.
- [ ] **Step 5: Smoke-test local development (Review Focus 3).**
  - Start `pnpm --filter @kurze-url/web dev` in the background. `curl -s -o /dev/null -w '%{http_code}' http://localhost:3000/` must print `200` or a `3xx` code. Then stop the server.
  - Run `pnpm --filter @kurze-url/web exec storybook dev -p 6006 --ci --smoke-test`: exit 0.
- [ ] **Step 6: Commit**, `pnpm format` first: `build: run app scripts through vp`.

### Task 4: Hooks, editor, CI and documentation

**Files:**

- Modify:
  - `lefthook.yml` (pre-commit `format` and `lint` jobs);
  - `.zed/settings.json` (the `lsp.oxfmt` settings and the Tailwind stylesheet path);
  - `.github/workflows/ci-js.yml` (comments only, unless Step 3 finds a direct tool call);
  - `CLAUDE.md`.

**Interfaces:**

- Consumes: the scripts from Task 3 and the root `vite.config.ts`.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Point the pre-commit jobs at `vp`.** Keep the globs and every comment.
  - `format` runs `pnpm exec vp fmt --check --no-error-on-unmatched-pattern {staged_files}`.
  - `lint` runs `pnpm exec vp lint --no-error-on-unmatched-pattern {staged_files}`.
  - The pre-push jobs call the scripts and stay as they are.
- [ ] **Step 2: Verify the hooks (Review Focus 1).** Hand the files to lefthook directly with `pnpm exec lefthook run pre-commit --file <path>`, repeating `--file` for each path. Check the flag's exact name with `lefthook run --help`. Never stage with `git add`; GitButler owns the index.
  - Run it with `--file apps/web/src/routeTree.gen.ts --file apps/web/src/components/ui/button.tsx`. Expected: exit 0.
  - Then create a probe `apps/web/src/hook-probe.ts` containing `export const digits = /[0-9]/u;` and run it with `--file apps/web/src/hook-probe.ts`. Expected: non-zero, naming `regexp/prefer-d`.
  - Delete the probe.
- [ ] **Step 3: Check CI.** Run `grep -nE "oxlint|oxfmt|vitest|vite build" .github/workflows/*.yml`. Every hit must be a comment, or a call through the `pnpm` scripts. Update the comment near line 103 of `ci-js.yml` to name Vite+. Do not rename any job.
- [ ] **Step 4: Update Zed.** In `.zed/settings.json`:
  - Add `"fmt.configPath": "./vite.config.ts"` to `lsp.oxfmt.initialization_options.settings`, next to `"run": "onSave"`.
  - Fix the Tailwind language server's stylesheet. The key under `lsp.tailwindcss-language-server.settings."experimental.configFile"` reads `apps/web/src/app/globals.css`, a file that does not exist. Change it to `apps/web/src/styles/app.css`, the stylesheet the `fmt` and `lint` blocks name, and keep the value `apps/web/src/**`. The maintainer asked for this fix on 2026-10-17.

  Leave the rest of the file alone.

- [ ] **Step 5: Update CLAUDE.md** in the entries the spec's Documentation section names:
  - the shadcn generated-files entry: `generated.config.ts` is now imported by the root `vite.config.ts`, as the `ignorePatterns` of both its `lint` and `fmt` blocks;
  - the `routeTree.gen.ts` entry, which regenerates through `vp build` or `vp dev`;
  - the Sentry source-map entry, only if the plugin order changed;
  - the Renovate entry's `pnpm update` advice, if `pnpm why` changed what it says;
  - the TypeScript codegen entry, only where it names a tool that moved.

  Edit only lines that name `oxlint.config.ts`, `oxfmt.config.ts`, `vite build` or `vitest`, and keep the existing tone. Use `grep -n`, never a full diff, on this file.

- [ ] **Step 6: Commit**, `pnpm format` first, as two commits:
  1. `build: point hooks and editor at vite+`: `lefthook.yml`, `.zed/settings.json` and the workflow comment. The commit body names the Tailwind stylesheet fix.
  2. `docs: describe the vite+ toolchain`: `CLAUDE.md`.

### Delivering pull request 1 (controller)

- Run the whole-branch review.
- Push with `but push`, then open a **draft** PR through the `create-pr` skill. Its description lists:
  - Rulings 1, 2, 5 and 8;
  - every REVIEW finding from Task 1;
  - every unsilenced finding from Task 2;
  - the new rule from decision 11.
- Bind the PR and turn on Auto-fix.
- Mark it ready only when every job is green, including `web unit tests` and `e2e against the preview`. Kodiak merges it.
- After the merge, run `but pull`, and update the memory note `js-ts-lint-format-tooling.md` to say Vite+.
- Report Ruling 8 (`@tanstack/nitro-v2-vite-plugin`) to the maintainer as a follow-up.

---

## Pull request 2: JS dependency updates

The controller creates `build/js-dependency-refresh` from the updated `main`.

### Task 5: Replace the removed `getResponse` and update TanStack

**Files:**

- Modify:
  - `apps/web/src/server/session.ts`: `flushSessionCookies`, near line 163, and its comment, near lines 134–160;
  - `apps/web/package.json` and `pnpm-lock.yaml`.
- Test:
  - `apps/web/src/server/session.test.ts`;
  - the getResponse mocks in `auth.test.ts`, `links.test.ts`, `members.test.ts`, `audit-log.test.ts`, `teams.test.ts`, `tags.test.ts`, `domains.test.ts` and `folders.test.ts`, all under `apps/web/src/server/`.

**Interfaces:**

- Consumes: `appendResponseHeader(name: ResponseHeaderName, value: string | Array<string>): void` from `@tanstack/react-start/server`.
- Produces: `flushSessionCookies(headers: Readonly<Headers>): void`, with an unchanged signature and an unchanged `createServerOnlyFn` wrapper.

- [ ] **Step 1: Check that the new API exists today.** Run `grep -rn "appendResponseHeader" apps/web/node_modules/@tanstack/react-start/dist apps/web/node_modules/@tanstack/start-server-core/dist --include='*.d.ts' | head -3`.
  - If it exists on the installed 1.168.x, Steps 2–5 run before the upgrade in Step 6.
  - Otherwise, run Step 6 first.
- [ ] **Step 2: Write the failing tests** in `session.test.ts`.
  - Change the module mock so it exposes **only** `appendResponseHeader`: `vi.mock('@tanstack/react-start/server', () => ({ appendResponseHeader: mocks.appendResponseHeader }))`. Any call to `setResponseHeader` or `getResponse` then throws, which pins "never overwritten".
  - Write these tests:

  ```ts
  it('hands every set-cookie value to appendResponseHeader in one call', () => {
  	const headers = new Headers();
  	headers.append('set-cookie', 'a=1; Path=/');
  	headers.append('set-cookie', 'b=2; Path=/');
  	flushSessionCookies(headers);
  	expect(mocks.appendResponseHeader).toHaveBeenCalledExactlyOnceWith('set-cookie', [
  		'a=1; Path=/',
  		'b=2; Path=/',
  	]);
  });

  it('appends again on a second flush instead of replacing', () => {
  	flushSessionCookies(new Headers([['set-cookie', 'a=1; Path=/']]));
  	flushSessionCookies(new Headers([['set-cookie', 'b=2; Path=/']]));
  	expect(mocks.appendResponseHeader).toHaveBeenNthCalledWith(1, 'set-cookie', ['a=1; Path=/']);
  	expect(mocks.appendResponseHeader).toHaveBeenNthCalledWith(2, 'set-cookie', ['b=2; Path=/']);
  });

  it('writes nothing when there is no set-cookie header', () => {
  	flushSessionCookies(new Headers());
  	expect(mocks.appendResponseHeader).not.toHaveBeenCalled();
  });
  ```

  Replace the existing tests that assert on `getResponse().headers.append`. Keep every other test in the file.

- [ ] **Step 3: Run them.** Run `pnpm --filter @kurze-url/web test -- src/server/session.test.ts`. Expected: FAIL, because `getResponse` is not a function under the new mock.
- [ ] **Step 4: Implement.** Import `appendResponseHeader` instead of `getResponse`. Inside `flushSessionCookies`, read `headers.getSetCookie()` and return early when it is empty; otherwise call `appendResponseHeader('set-cookie', cookies)` once.
  - Rewrite the long comment so it explains:
    - why `appendResponseHeader` and not `setResponseHeader`: the latter replaces every cookie;
    - that values with the same name, domain and path collapse to the last, as a browser would apply them (Ruling 4).
  - Keep the `createServerOnlyFn` paragraph.
- [ ] **Step 5: Move the other eight mocks.** In each of the eight test files, replace the `getResponse` entry in the `vi.hoisted` block and in `vi.mock` with `appendResponseHeader: vi.fn()`. Assertions on `headers.append` move to `appendResponseHeader` (for example `domains.test.ts` near line 116, `links.test.ts` near line 207). Then:

  | Command | Expected |
  | --- | --- |
  | `pnpm --filter @kurze-url/web test` | PASS, same count as before plus the new tests |
  | `grep -rn "getResponse" apps/web/src` | no output, after the doc comments in `audit-log.ts`, `links.ts` and `auth.ts` that mention it are reworded to name `flushSessionCookies` |

- [ ] **Step 6: Update the TanStack family together.**
  - Run `pnpm --filter @kurze-url/web update @tanstack/react-start @tanstack/react-router @tanstack/react-router-devtools @tanstack/react-router-ssr-query`, plus every other `@tanstack/*` that `outdated-js.json` lists.
  - Then run `pnpm update -r --depth 99 @tanstack/start-client-core`.
  - Expected:
    - `pnpm why @tanstack/router-core` shows one version;
    - `pnpm typecheck`, the unit tests, `test:storybook` and `build` pass;
    - the routeTree tail still holds `interface Register`.
- [ ] **Step 7: Commit**, `pnpm format` first, as two commits:
  1. `fix(web): replace removed getResponse`: Steps 2–5.
  2. `build(deps): update tanstack start to 1.169`: Step 6. Include `routeTree.gen.ts` only if it changed.

### Task 6: Update the remaining JS dependencies

**Files:**

- Modify:
  - `package.json`, `apps/web/package.json`, `packages/api-client/package.json`, `pnpm-lock.yaml`;
  - `apps/web/src/lib/observability.ts`, near line 267;
  - `apps/web/src/lib/observability.test.ts`, near lines 332 and 371.

**Interfaces:**

- Consumes: Task 5's tree.
- Produces: a tree where `pnpm outdated -r` lists nothing except `typescript` 7 in `packages/api-client`, plus whatever `vite-plus` pins and the gate holds back.

- [ ] **Step 1: Re-run the inventory.** Run `pnpm outdated -r` and list every entry in your report.
- [ ] **Step 2: Update Sentry and pin its new field.**
  - Run `pnpm --filter @kurze-url/web update @sentry/tanstackstart-react`, then `pnpm typecheck`. Expected: FAIL at `satisfies ExplicitDataCollection`, naming the new field(s).
  - Add each named field to the expected key set and the `toStrictEqual` object in `observability.test.ts`, with its off-state value: the value that collects nothing, as v10's `sendDefaultPii: false` did.
  - Run `pnpm --filter @kurze-url/web test -- src/lib/observability.test.ts`. Expected: FAIL.
  - Set the same value in `observability.ts`. Expected: typecheck and that test pass.
  - If a field's off-state is not clear from the SDK's type documentation, stop and report.
- [ ] **Step 3: Update everything else** with `pnpm update -r`, keeping each package within its current major. The exceptions are `typescript` in `packages/api-client` (stays on 6) and anything `vite-plus` pins.
  - The gate applies, and is never bypassed.
  - `lucide-react`: `pnpm typecheck` names any renamed icon. Replace it with its new name and the same glyph.
- [ ] **Step 4: Verify.** Each of these must pass:
  - `pnpm format:check`, `pnpm lint` and `pnpm typecheck`;
  - the unit tests and `test:storybook`, with counts at least the baseline's;
  - `build` and `build-storybook`;
  - `pnpm why @tanstack/router-core`: one version;
  - `pnpm install --frozen-lockfile`.
- [ ] **Step 5: Commit**, `pnpm format` first: `build(deps): update js dependencies`. The Sentry field goes in the same commit, because the update requires it.

### Delivering pull request 2 (controller)

Same as pull request 1, except that it is not a draft. The description lists:

- the updated packages;
- the Sentry field;
- Ruling 4, the cookie collapse.

---

## Pull request 3: Go dependency updates

The controller creates `build/go-dependency-refresh` from the updated `main`.

### Task 7: Update `apps/api`'s modules

**Files:**

- Modify: `apps/api/go.mod`, `apps/api/go.sum`.
- Modify, only if the compiler requires it: `apps/api/internal/observability/sloghandler_test.go`, the `fakeTransport`.

**Interfaces:**

- Consumes: nothing from earlier tasks.
- Produces: nothing later tasks depend on.

- [ ] **Step 1: Update the modules.** From `apps/api`:

  ```bash
  go get github.com/getsentry/sentry-go@latest github.com/go-chi/chi/v5@latest github.com/redis/go-redis/v9@latest golang.org/x/crypto@latest golang.org/x/net@latest golang.org/x/sync@latest google.golang.org/protobuf@latest
  go mod tidy
  ```

  Then run `go list -m -u all | grep '\['`. Expected: no direct dependency except `github.com/piglig/go-qr`. Update any other direct one the same way.

- [ ] **Step 2: Check the sentry-go 0.50 transport.** Run `go vet ./...`.
  - If `fakeTransport` no longer satisfies `sentry.Transport`, add the missing methods.
  - Keep its `flushes` counter incremented in both `Flush` and `FlushWithContext`.
- [ ] **Step 3: Verify.** From `apps/api`, each must pass:
  - `go build ./...` and `go vet ./...`;
  - `golangci-lint run`, with 0 issues;
  - `go test ./internal/observability/ ./internal/scanning/ ./internal/qr/ -count=1`.

  The full `go test ./...` needs Postgres and Redis, and runs in CI.

- [ ] **Step 4: Commit:** `build(deps): update go dependencies`. The pre-push hook's `go vet` and `gofmt` must pass.

### Delivering pull request 3 (controller)

Same as pull request 2. The description lists each module's old and new version, and any transport change.
