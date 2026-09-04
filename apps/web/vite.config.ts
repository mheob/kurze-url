import tailwindcss from '@tailwindcss/vite';
import { devtools } from '@tanstack/devtools-vite';
import { nitroV2Plugin } from '@tanstack/nitro-v2-vite-plugin';
import { tanstackStart } from '@tanstack/react-start/plugin/vite';
import viteReact from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

const config = defineConfig({
	// `tanstackStart` alone emits a plain Vite build: `dist/client` plus a
	// `dist/server/server.js` that nothing knows how to run. Nitro turns that
	// server bundle into whatever the host expects — on Vercel, a serverless
	// function plus static assets under `.vercel/output`. Without it Vercel's
	// TanStack Start preset (`vite build`, output `dist`) publishes the build
	// as static files, and every route 404s because SSR never runs.
	//
	// The preset is left to Nitro's own host detection rather than pinned to
	// `vercel`, so `pnpm build` stays a Node server locally. compatibilityDate
	// pins the host defaults Nitro resolves against, so a future Nitro release
	// can change them for new projects without silently changing this build.
	plugins: [
		devtools(),
		tailwindcss(),
		tanstackStart(),
		nitroV2Plugin({ compatibilityDate: '2026-09-04' }),
		viteReact(),
	],
	resolve: { tsconfigPaths: true },
});

export default config;
