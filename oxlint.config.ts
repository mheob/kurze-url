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
// its own — it lives in apps/web's node_modules). Every rule needs both options repeated
// because oxlint's top-level `settings` only forwards its own known plugin keys (react,
// jsx-a11y, next, jest, jsdoc, vitest) — a `better-tailwindcss` entry there is silently
// dropped, never reaching the plugin.
const tailwindPluginOptions = { cwd: 'apps/web', entryPoint: 'src/styles/app.css' };

// `dark` is applied to `<html>` as a plain toggle class (see Task 6's root route) so that
// `app.css`'s `@custom-variant dark (&:where(.dark, .dark *))` can key off it. It is a
// selector hook, not a generated Tailwind utility, so `no-unknown-classes`'s "is this a real
// class" check can't recognise it and flags it as unknown. Anchored so it only matches the
// bare class, never a real `dark:`-prefixed utility.
const noUnknownClassesOptions = { ...tailwindPluginOptions, ignore: ['^dark$'] };

export default defineConfig({
	// baseJsConfig already extends baseConfig — literally the same object, verified
	// by comparing the resolved rule sets: 529 rules either way, no severity
	// changes. Listing both would be redundant, not additive.
	extends: [baseJsConfig, reactConfig, storybookConfig, tailwindcssConfig],
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
		},
	],
	rules: {
		'better-tailwindcss/enforce-canonical-classes': ['warn', tailwindPluginOptions],
		'better-tailwindcss/enforce-consistent-class-order': ['warn', tailwindPluginOptions],
		'better-tailwindcss/enforce-consistent-line-wrapping': ['warn', tailwindPluginOptions],
		'better-tailwindcss/no-conflicting-classes': ['error', tailwindPluginOptions],
		'better-tailwindcss/no-deprecated-classes': ['warn', tailwindPluginOptions],
		'better-tailwindcss/no-duplicate-classes': ['warn', tailwindPluginOptions],
		'better-tailwindcss/no-unknown-classes': ['error', noUnknownClassesOptions],
		'better-tailwindcss/no-unnecessary-whitespace': ['warn', tailwindPluginOptions],
	},
});
