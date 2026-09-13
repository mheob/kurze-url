import { baseConfig } from '@mheob/oxfmt-config';
import { defineConfig } from 'oxfmt';

import { generatedFiles } from './generated.config.ts';

export default defineConfig({
	...baseConfig,
	// Shared with oxlint. See generated.config.ts for why both tools skip these.
	ignorePatterns: generatedFiles,
	// `sortTailwindcss: true` (from baseConfig) defaults its v4 stylesheet to the one shipped
	// inside the installed `tailwindcss` package — the stock theme, which knows nothing about
	// this app's `@theme` block. Every custom colour was therefore an unknown class, and an
	// unknown class sorts to the front: `border-border flex items-center …` instead of
	// Tailwind's own `flex items-center … border-border`. Pointing it at `app.css` is what
	// makes oxfmt and oxlint's better-tailwindcss agree on one order; while they disagreed,
	// `oxlint --fix` and `pnpm format` reversed each other's work on every run.
	//
	// The path is relative to THIS file, not to apps/web. `functions` lists the helpers whose
	// arguments hold class names — `cn` and `cva` are shadcn's, in `ui/button.tsx`.
	sortTailwindcss: {
		functions: ['cn', 'cva'],
		stylesheet: 'apps/web/src/styles/app.css',
	},
});
