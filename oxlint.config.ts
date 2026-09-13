import {
	baseJsConfig,
	reactConfig,
	storybookConfig,
	tailwindcssConfig,
} from '@mheob/oxlint-config';
import { defineConfig } from 'oxlint';

import { generatedFiles } from './generated.config.ts';

// better-tailwindcss resolves its `tailwindcss` install and CSS entry point relative to a
// `cwd` it defaults to the process cwd (the repo root, which has no `tailwindcss` package of
// its own — it lives in apps/web's node_modules).
//
// Both options are repeated on every rule below, and still have to be: oxlint's top-level
// `settings` only forwards its own known plugin keys, so a `better-tailwindcss` entry there
// is silently dropped. `@mheob/oxlint-config` v4 passes them that way — which is why its
// rule severities are kept but its options are overridden here. Relying on the shared
// config alone makes oxlint print "Tailwind CSS is not installed. Disabling rule
// better-tailwindcss/…" eight times and lint every class name as if the plugin were absent;
// with the options restored, a bogus class is an error again.
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
			plugins: ['promise', 'unicorn'],
			rules: {
				// `always-return` cannot see a `void`. Both `.then()` chains in this
				// app — `copy-button.tsx` and `login.tsx`, the only two outside tests
				// — are voided terminal side effects where returning a value would
				// mean nothing. It started firing only because
				// `no-confusing-void-expression`'s fix turned their one-expression
				// arrows into block bodies. If a consuming chain is ever written
				// here, turn this back on.
				'promise/always-return': 'off',
				'unicorn/no-useless-undefined': 'off',
				'unicorn/prefer-dom-node-append': 'off',
				'unicorn/prefer-dom-node-text-content': 'off',
				'unicorn/prefer-spread': 'off',
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
			plugins: ['react'],
			rules: { 'react/jsx-curly-brace-presence': 'off' },
		},
	],
});
