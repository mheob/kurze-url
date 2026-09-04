import { baseConfig, baseJsConfig } from '@mheob/oxlint-config';
import { defineConfig } from 'oxlint';

// `reactConfig`, `storybookConfig` and `tailwindcssConfig` get added once apps/web exists.
export default defineConfig({
	extends: [baseConfig, baseJsConfig],
	// Generated from apps/api/openapi.json by @hey-api/openapi-ts, including the
	// vendored request runtime. Linting it would report on the generator's
	// output style, which nobody here can act on without editing files that are
	// overwritten on every regeneration.
	ignorePatterns: ['packages/api-client/src/generated/**'],
});
