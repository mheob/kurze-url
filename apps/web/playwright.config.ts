import { defineConfig } from '@playwright/test';

/**
 * BASE_URL is set by CI to the pull request's Vercel preview, per planning doc
 * 07. Locally it falls back to a build served on 3000, so the same specs run
 * in both places without a second configuration.
 */
const baseURL = process.env.BASE_URL ?? 'http://localhost:3000';

export default defineConfig({
	testDir: './e2e',
	use: { baseURL },
	webServer: process.env.BASE_URL
		? undefined
		: { command: 'pnpm build && pnpm start', port: 3000, reuseExistingServer: true },
});
