import react from '@vitejs/plugin-react';
import { configDefaults, defineConfig } from 'vitest/config';

export default defineConfig({
	plugins: [react()],
	test: {
		environment: 'jsdom',
		globals: true,
		setupFiles: ['./src/test/setup.ts'],
		// Vitest's own default `exclude` doesn't know about `e2e/`, and its default
		// `include` pattern matches `*.spec.ts` — the same suffix Playwright specs use
		// (Task 11) — so without this, `vitest run` would pick up and fail on
		// Playwright-only files instead of leaving them to `playwright test`.
		exclude: [...configDefaults.exclude, 'e2e/**'],
	},
});
