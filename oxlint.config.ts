import {
	baseJsConfig,
	reactConfig,
	storybookConfig,
	tailwindcssConfig,
} from '@mheob/oxlint-config';
import { defineConfig } from 'oxlint';

import { generatedFiles } from './generated.config.ts';

// better-tailwindcss resolves its `tailwindcss` install and CSS entry point relative to a
// `cwd` it defaults to the process cwd — the repo root, which has no `tailwindcss` package
// of its own, because it is a dependency of apps/web and pnpm only symlinks declared ones.
//
// These options have to be set on THIS config's own top-level `settings`, and repeating
// them here rather than relying on `tailwindcssConfig()`'s own `settings` is the whole
// point: oxlint merges `rules` out of an extended config but NOT `settings`. So
// `@mheob/oxlint-config` v4, which passes them exactly that way, gets its eight rule
// severities honoured and its settings silently dropped — oxlint then prints "Tailwind CSS
// is not installed. Disabling rule better-tailwindcss/…" eight times and lints no class
// name at all. Proven by probe: identical options in a top-level `settings` block activate
// the plugin, the same block inside an `extends` entry does not.
//
// The warnings are the only symptom, and they are easy to read past — every one of these
// eight rules was off for the project's whole life until 2026-09-13 because of it. If they
// come back, this block stopped being applied.
const tailwindPluginOptions = { cwd: 'apps/web', entryPoint: 'src/styles/app.css' };

// `dark` is applied to `<html>` as a plain toggle class (see Task 6's root route) so that
// `app.css`'s `@custom-variant dark (&:where(.dark, .dark *))` can key off it. It is a
// selector hook, not a generated Tailwind utility, so `no-unknown-classes`'s "is this a real
// class" check can't recognise it and flags it as unknown. Anchored so it only matches the
// bare class, never a real `dark:`-prefixed utility.
const ignoredClasses = ['^dark$'];

export default defineConfig({
	// baseJsConfig already extends baseConfig — literally the same object, verified
	// by comparing the resolved rule sets: 529 rules either way, no severity
	// changes. Listing both would be redundant, not additive.
	extends: [
		baseJsConfig,
		reactConfig,
		storybookConfig,
		tailwindcssConfig({ ignoredClasses, options: tailwindPluginOptions }),
	],
	// Shared with oxfmt. See generated.config.ts for why both tools skip these.
	ignorePatterns: generatedFiles,
	overrides: [
		{
			// scripts/ holds Node CLI entry points, not application code, and eight
			// rules in the shared config assume the latter. Each is switched off for
			// its own reason rather than as a blanket exemption:
			//
			// no-sync — a startup script is sequential by definition. Reading two
			//   dotenv files asynchronously would add ceremony and no concurrency,
			//   because nothing else can run until the checks pass.
			// no-process-env — reading and composing the environment is what these
			//   scripts are for; the rule exists to keep config out of app code.
			// no-top-level-await — the rule protects `require(esm)` interop. Nothing
			//   imports a file under scripts/; Node runs it directly.
			// avoid-new — Node's socket and timer APIs are callback-based and have no
			//   promise equivalents worth the indirection here.
			// no-await-in-loop — polling a port until it answers is sequential on
			//   purpose. Promise.all would fire every attempt at once.
			// no-magic-numbers is NOT disabled: timeouts are named constants.
			files: ['scripts/**/*.ts'],
			plugins: ['node', 'promise'],
			rules: {
				'eslint/no-await-in-loop': 'off',
				'node/no-process-env': 'off',
				'node/no-sync': 'off',
				'node/no-top-level-await': 'off',
				'promise/avoid-new': 'off',
			},
		},
		{
			files: ['apps/web/**/*.tsx'],
			// reactConfig turns on react, react-perf and typescript, but not
			// jsx-a11y. Accessibility is a project requirement rather than a
			// preference, and this is the cheapest of the three levels that check
			// it: it fires in the editor, before a commit exists.
			//
			// `react` is repeated here even though reactConfig's own override already
			// enables it for `**/*.tsx`: overrides matching the same file are NOT
			// deep-merged into one rule set — each override is independent, and a
			// `rules` entry only takes effect for a plugin this override activates
			// itself. Without redeclaring `react` here, the `jsx-no-literals` override
			// below is silently dropped and the rule falls back to its `warn` default
			// from baseConfig's `categories` block — proven with the probe in Step 4.
			plugins: ['jsx-a11y', 'react'],
			rules: {
				// The fastest half of the no-hardcoded-string rule. It sees JSX text
				// children only — a hardcoded aria-label or a string inside an
				// expression container is invisible to it, which is why the
				// catalogue-parity and rendered-divergence checks in Tasks 5 and 11
				// carry the other half.
				//
				// `allowedStrings`, not `allowStrings`: oxlint's native rule schema
				// rejects the latter as an unknown field (confirmed by trying it — it
				// throws "Failed to build configuration" once the rule is actually
				// active, rather than silently ignoring the typo).
				'react/jsx-no-literals': ['error', { allowedStrings: [], ignoreProps: false }],
			},
		},
		{
			files: ['apps/web/**/*.test.ts', 'apps/web/**/*.test.tsx'],
			plugins: ['vitest'],
			rules: {
				// Both fixes below are applied by `pnpm lint:fix`, and both were
				// observed breaking this suite on 2026-09-13.
				//
				// `prefer-called-with` rewrites `toHaveBeenCalled()` into
				// `toHaveBeenCalledWith()`, which is not a stricter form of the same
				// check — it asserts the mock was called with NO arguments. That
				// compiles and reads almost identically, so only the test run caught
				// it. Wanting an argument assertion is reasonable; inventing one
				// silently is not.
				//
				// `prefer-import-in-mock` rewrites `vi.mock('./x', factory)` into
				// `vi.mock(import('./x'), factory)`. That form is real and type-safe,
				// and that is the problem: it checks the factory against the whole
				// module, so all sixteen partial mocks here become type errors.
				// Adopting it is a refactor of what those mocks replace, not a lint
				// fix.
				'vitest/prefer-called-with': 'off',
				'vitest/prefer-import-in-mock': 'off',
			},
		},
		{
			// The e2e specs are Playwright. They match the shared config's
			// `**/*.spec.ts` glob, so its vitest rules reach them legitimately by
			// the glob and wrongly in substance — a naming collision, not a
			// misconfiguration on either side.
			//
			// `no-importing-vitest-globals` is repeated from the shared config's own
			// setting. Overrides are not deep-merged, so activating the plugin here
			// without restating it would resurrect a rule the shared config
			// deliberately turns off, and it would then strip the vitest imports out
			// of files that need them.
			files: ['apps/web/e2e/**'],
			plugins: ['vitest'],
			rules: {
				'vitest/consistent-test-filename': 'off',
				'vitest/no-conditional-in-test': 'off',
				'vitest/no-importing-vitest-globals': 'off',
				'vitest/prefer-each': 'off',
				// Its auto-fix inserted `import { expect, test } from 'vitest'` at the
				// top of all four specs, which already take `expect` from
				// `@playwright/test` and `test` from `./fixtures/auth`: duplicate
				// identifiers, and a tree that no longer typechecked.
				'vitest/prefer-importing-vitest-globals': 'off',
			},
		},
		{
			// Four auto-fixes that do not survive contact with this code. Scoped to
			// `unicorn` and `promise` only — activating any further plugin here
			// would pull its rules into files the shared config had scoped them away
			// from, which is how an earlier attempt at this block dragged
			// `react-hooks` into the Playwright fixtures.
			//
			// `no-useless-undefined` strips arguments that are required:
			// `mockResolvedValue(undefined)` became `mockResolvedValue()`, and
			// `toHaveBeenCalledExactlyOnceWith(undefined)` quietly became an
			// assertion about no arguments at all.
			//
			// `prefer-spread` turned `Array.from(password)` into `[...password]`,
			// which `no-misused-spread` then reports as an error in its own right —
			// spreading a string splits code points and breaks complex characters.
			//
			// `prefer-dom-node-append` swapped `doc.body.appendChild(anchor)` for
			// `append`, which returns nothing where `appendChild` returns the node,
			// leaves the download helper pairing `append` with `removeChild`, and
			// misses the spy the QR download test asserts through. It typechecks
			// perfectly.
			//
			// `prefer-dom-node-text-content` assumes a DOM node. On a Playwright
			// `Locator` it swaps two different methods with different return types:
			// `innerText()` is `string`, `textContent()` is `string | null`.
			files: ['apps/web/**'],
			plugins: ['promise', 'typescript', 'unicorn'],
			rules: {
				// `always-return` cannot see a `void`. Both `.then()` chains in this
				// app — `copy-button.tsx` and `login.tsx`, the only two outside tests
				// — are voided terminal side effects where returning a value would
				// mean nothing. It started firing only because
				// `no-confusing-void-expression`'s fix turned their one-expression
				// arrows into block bodies. If a consuming chain is ever written
				// here, turn this back on.
				// Two plugins report this same rule, so 42 places produce 84 warnings,
				// and every one of them appeared only because
				// `strict-boolean-expressions` was satisfied: making `if (message)`
				// explicit as `message !== undefined` turns it into a "negated
				// condition". Satisfying one enabled rule violates another.
				//
				// None of the 42 is the `if (!x) {...} else {...}` shape the rule
				// exists for — all are ternaries, and 27 are conditional renders of
				// the form `value !== null ? <p/> : null`. Flipping those puts the
				// null branch first, which reads worse than the React idiom they
				// already use.
				'eslint/no-negated-condition': 'off',
				// `no-void` forbids exactly what `no-misused-promises` requires. Every
				// `void` in this app discards a promise on purpose — `void
				// router.navigate(...)`, `void handleSubmit(event)` — which is the
				// documented way to say "not awaited, deliberately". With both rules
				// on there is no spelling that satisfies both.
				'eslint/no-void': 'off',
				// Fourteen of the eighteen sites this rule reports are build and test
				// tooling — vite.config, playwright.config, the e2e global setup and
				// its fixtures — where reading the environment directly is the whole
				// job and a config module would be indirection for its own sake.
				//
				// The other four are `API_HOST` and the Vercel bypass secret in
				// `server/api.ts`, and the two Supabase values in `server/supabase.ts`.
				// Each already sits in the single function that owns that variable and
				// validates it there: `createSupabase` throws when either value is
				// missing, `apiBaseUrl` falls back to the related-project lookup, and
				// CLAUDE.md documents the `API_HOST` pin at exactly that call site.
				// Moving four reads behind a module would relocate them without
				// centralising a decision. Revisit if the count grows.
				'node/no-process-env': 'off',
				// TanStack Router signals navigation by throwing: `throw redirect({...})`
				// and `throw notFound()` are its control flow, and neither is an
				// Error. The rule is right in general and wrong for this framework,
				// and the alternative — wrapping a router signal in an Error — would
				// stop the router recognising it.
				// This rule disagrees with the project's own type checking. It reports
				// `rawKey?.trim()` in `lib/preferences.ts` as an unnecessary optional
				// chain, but the repository sets `noUncheckedIndexedAccess`, so a
				// destructured array element really is `string | undefined` - removing
				// the `?.` was tried and `pnpm typecheck` answered
				// "TS18048: 'rawKey' is possibly 'undefined'". Ten of its twelve
				// findings are that same pattern, including the `rows[0]` guard in the
				// e2e fixture.
				//
				// The remaining two are runtime guards deliberately kept beyond what
				// the types promise: `restyleQrSvg` checks `documentElement` because
				// its contract is to fail safe on anything that is not an SVG, which
				// the DOM types cannot express about parser output.
				//
				// A rule whose advice the type checker rejects cannot be acted on
				// mechanically, so it is off rather than warning.
				'typescript/no-unnecessary-condition': 'off',
				'unicorn/no-negated-condition': 'off',
				'unicorn/no-useless-undefined': 'off',
			},
		},
		{
			// TanStack's file-route convention puts `export const Route =
			// createFileRoute(...)` at the top of the module and the component it
			// names below it. That is what every example in the router's own
			// documentation looks like, and it is where a reader goes to find what a
			// route does.
			//
			// Both rules here object to it. `no-use-before-define` fires on
			// `component: RouteComponent` referencing a function declared lower down
			// — ten of its eleven findings are exactly that, and it works because
			// function declarations hoist. `exports-last` wants `Route` moved to the
			// bottom, which inverts the file.
			//
			// Satisfying them is not merely unidiomatic, it is hazardous: an attempt
			// at exactly this reordering on 2026-09-13 removed 97 lines from
			// `_authed.tsx` — every export, including the `Membership` type five
			// other modules import — and the file had to be restored from the last
			// commit. Both rules stay on everywhere else in the app.
			files: ['apps/web/src/routes/**'],
			plugins: ['import'],
			rules: {
				'eslint/no-use-before-define': 'off',
				'import/exports-last': 'off',
			},
		},
		{
			// Scoped to .tsx, matching the jsx-a11y override above: `react` must not
			// be activated for plain .ts files, or `react-hooks/rules-of-hooks`
			// reaches `e2e/fixtures/auth.ts`, where Playwright's `use` fixture
			// parameter reads as a React hook call.
			//
			// `jsx-curly-brace-presence` unwrapped `<p>{'marker'}</p>` into
			// `<p>marker</p>`, which this project's own error-level
			// `react/jsx-no-literals` then rejects — golden rule 6 forbids a
			// hardcoded user-facing string, and the expression container was there
			// to satisfy it. One rule's fixer breaking a rule the project
			// deliberately set to error.
			files: ['apps/web/**/*.tsx'],
			plugins: ['react', 'react-perf'],
			rules: {
				// Both react-perf rules exist to stop a new prop identity on every
				// render from defeating a memoised child. That premise does not hold
				// here: there is no `React.memo` anywhere in apps/web and no
				// `useCallback` at all — grep before re-enabling. Every child
				// re-renders regardless, so wrapping 58 inline handlers would buy no
				// avoided render and cost 58 dependency arrays to keep correct, which
				// is where stale-closure bugs come from. React's own guidance is not
				// to memoise by default. If a list ever measures slow, memoise that
				// list deliberately rather than turning this back on wholesale.
				//
				// Sorted before the `react/*` entries on purpose: eslint(sort-keys)
				// compares the whole key, and `-` sorts before `/`.
				'react-perf/jsx-no-new-array-as-prop': 'off',
				'react-perf/jsx-no-new-function-as-prop': 'off',
				'react-perf/jsx-no-new-object-as-prop': 'off',
				'react/jsx-curly-brace-presence': 'off',
				// Fires on every TanStack route file, because `createFileRoute`
				// requires the module to export `Route`, and most of them export a
				// loader beside it. The rule asks for a file that exports only
				// components; the router asks for the opposite. The one non-route
				// case is `ui/button.tsx`, where `buttonVariants` beside the
				// component is shadcn's own shape.
				'react/only-export-components': 'off',
			},
		},
	],
	rules: {
		// oxfmt owns line layout, and this rule is a second formatter that works
		// *inside* class strings — the two cannot both win. With the rule's
		// `printWidth` aligned to oxfmt's 100 and `preferSingleLine` on, 11 of its
		// 19 findings go away and the remaining 8 are all in `ui/button.tsx`, whose
		// shadcn variant strings genuinely exceed 100 columns. Applying its fix
		// there rewrites the string into a wrapped template literal, and the very
		// next `pnpm format` collapses it back onto one line — verified by running
		// exactly that sequence. Since `pnpm format` runs in the Lefthook pre-commit
		// hook, oxfmt always has the last word, so leaving the rule on would fail
		// every commit over a wrap nothing is allowed to keep.
		//
		// The other seven better-tailwindcss rules stay on. `enforce-consistent-
		// class-order` in particular now agrees with oxfmt, because oxfmt.config.ts
		// points `sortTailwindcss` at the same `app.css` this file's `entryPoint`
		// names — that agreement is what makes keeping it worthwhile.
		'better-tailwindcss/enforce-consistent-line-wrapping': 'off',
	},
	settings: { 'better-tailwindcss': tailwindPluginOptions },
});
