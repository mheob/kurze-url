import path from 'node:path';
import { fileURLToPath } from 'node:url';

import { storybookTest } from '@storybook/addon-vitest/vitest-plugin';
import react from '@vitejs/plugin-react';
import { playwright } from '@vitest/browser-playwright';
import { configDefaults, defineConfig } from 'vitest/config';

const dirname = path.dirname(fileURLToPath(import.meta.url));

export default defineConfig({
	plugins: [react()],
	test: {
		// Vitest's own default `exclude` doesn't know about `e2e/`, and its default
		// `include` pattern matches `*.spec.ts` — the same suffix Playwright specs use
		// (Task 11) — so without this, `vitest run` would pick up and fail on
		// Playwright-only files instead of leaving them to `playwright test`. Shared
		// at this (root) level because both projects below need it, and because
		// Vite's config merge concatenates array-valued keys rather than replacing
		// them — an `exclude` override in a project would add to this, not replace
		// it, so there is nothing to lose by sharing it.
		exclude: [...configDefaults.exclude, 'e2e/**'],
		projects: [
			{
				// The pre-existing suite, unchanged: jsdom, RTL, MSW. Named so
				// `--project unit` (the plain `test` script) can select it alone —
				// without a name filter, `vitest run` runs every project below too.
				//
				// `environment`/`globals`/`setupFiles` live here rather than at the
				// root: the same array-concatenation behaviour that makes sharing
				// `exclude` safe above makes it unsafe here. A root-level `setupFiles`
				// would be *inherited and concatenated* into the storybook project
				// below even though that project sets its own — proven by trying it:
				// the storybook project failed to load `src/test/setup.ts`'s `msw/node`
				// import (`node:http` externalized for browser compatibility) despite
				// the project explicitly setting `setupFiles: []`. Scoping these three
				// keys to only the project that needs them avoids the merge entirely.
				extends: true,
				test: {
					name: 'unit',
					environment: 'jsdom',
					globals: true,
					setupFiles: ['./src/test/setup.ts'],
				},
			},
			{
				extends: true,
				plugins: [storybookTest({ configDir: path.join(dirname, '.storybook') })],
				test: {
					name: 'storybook',
					// A real browser, not jsdom: this is what makes `a11y: { test: 'error' }`
					// in .storybook/preview.tsx actually fail a run (Finding 2) — jsdom has
					// no rendering engine for axe to inspect.
					browser: {
						enabled: true,
						headless: true,
						provider: playwright({}),
						instances: [{ browser: 'chromium' }],
					},
				},
			},
		],
	},
});
