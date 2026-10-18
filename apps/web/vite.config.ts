import { sentryTanstackStart } from '@sentry/tanstackstart-react/vite';
import tailwindcss from '@tailwindcss/vite';
import { devtools } from '@tanstack/devtools-vite';
import { tanstackStart } from '@tanstack/react-start/plugin/vite';
import viteReact from '@vitejs/plugin-react';
import { nitro } from 'nitro/vite';
import { defineConfig } from 'vite-plus';

/**
 * Only with a token, so a local build and a fork's build still work — the
 * plugin is not registered at all without it rather than registered and
 * failing.
 *
 * `filesToDeleteAfterUpload` is not optional tidying. Uploading the maps
 * makes stack traces readable in Sentry; leaving them in the deployed output
 * publishes this app's source next to its bundle, which is worse than the
 * minified traces the upload was meant to fix.
 *
 * The maps this deletes are the client's. Nitro 3 builds the client
 * environment straight into its public directory, so they land in
 * `.vercel/output/static` on Vercel and in `.output/public` for a local or
 * self-hosted `pnpm build` (Nitro's default). Each needs its own glob, because
 * a glob that misses a directory deletes nothing there; the third, `./dist`,
 * is explained where it stands. The server writes no maps at all: Nitro's
 * `sourcemap` is pinned off below and also governs the SSR build, which Nitro
 * bundles as one of its services, so the `hidden` setting this plugin applies
 * reaches the client alone.
 */
const sentryPlugins =
	process.env.SENTRY_AUTH_TOKEN !== undefined && process.env.SENTRY_AUTH_TOKEN !== ''
		? [
				sentryTanstackStart({
					authToken: process.env.SENTRY_AUTH_TOKEN,
					org: process.env.SENTRY_ORG,
					project: process.env.SENTRY_PROJECT,
					sourcemaps: {
						filesToDeleteAfterUpload: [
							// Not for Nitro 3, which writes nothing to `dist/`. Only for a build
							// in which Nitro never ran, the failure described above `plugins`:
							// Vercel's preset would then publish `dist`, maps included.
							'./dist/**/*.map',
							'./.output/**/*.map',
							'./.vercel/output/**/*.map',
						],
					},
				}),
			]
		: [];

const config = defineConfig({
	// Vercel sets VERCEL_ENV and VERCEL_GIT_COMMIT_SHA on the build, without
	// the VITE_ prefix Vite needs to expose a value to the browser bundle.
	// Defining them here keeps the release identical to the API's — one bad
	// deployment stays correlatable across both Sentry projects — without
	// two more variables to set by hand and keep in step.
	define: {
		'import.meta.env.VITE_SENTRY_ENVIRONMENT': JSON.stringify(
			process.env.VERCEL_ENV ?? 'development',
		),
		'import.meta.env.VITE_SENTRY_RELEASE': JSON.stringify(process.env.VERCEL_GIT_COMMIT_SHA ?? ''),
	},
	// `tanstackStart` alone emits a plain Vite build: `dist/client` plus a
	// `dist/server/server.js` that nothing knows how to run. Nitro 3's Vite
	// plugin takes over the build instead: it writes the client environment
	// straight into the host's static directory and bundles the SSR
	// environment into the server the host expects, so no `dist/` is written.
	// On Vercel that is `.vercel/output`, with static assets under `static/`
	// and one function, `functions/__server.func`; anywhere else it is
	// `.output`, whose `server/index.mjs` is what `start`, `preview` and
	// Playwright's `webServer` run. Without Nitro, Vercel's TanStack Start
	// preset (`vite build`, output `dist`) publishes the build as static files,
	// and every route 404s because SSR never runs.
	//
	// The preset is left to Nitro's own host detection rather than pinned to
	// `vercel`, so `pnpm build` stays a Node server locally. compatibilityDate
	// pins the host defaults Nitro resolves against, so a future Nitro release
	// can change them for new projects without silently changing this build.
	plugins: [
		devtools(),
		tailwindcss(),
		tanstackStart(),
		// `sourcemap: false` is Nitro 3's default, written out so that it stays
		// the answer. getsentry/sentry-javascript#25360, an open pull request,
		// makes the Sentry plugin turn Nitro's server maps on and upload them
		// unless a Nitro config sets `sourcemap`, so a routine, auto-merged
		// Sentry minor would otherwise overturn the 2026-10-03 decision recorded
		// in CLAUDE.md. That pull request's release is when to revisit it.
		nitro({ compatibilityDate: '2026-09-04', sourcemap: false }),
		viteReact(),
		// Last, as Sentry's own setup guide requires.
		...sentryPlugins,
	],
	resolve: { tsconfigPaths: true },
});

export default config;
