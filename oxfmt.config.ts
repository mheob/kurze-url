import { baseConfig } from '@mheob/oxfmt-config';
import { defineConfig } from 'oxfmt';

import { generatedFiles } from './generated.config.ts';

export default defineConfig({
	...baseConfig,
	// Shared with oxlint. See generated.config.ts for why both tools skip these.
	ignorePatterns: generatedFiles,
});
