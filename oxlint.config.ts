import { baseConfig, baseJsConfig } from '@mheob/oxlint-config';
import { defineConfig } from 'oxlint';

// `reactConfig`, `storybookConfig` and `tailwindcssConfig` get added once apps/web exists.
export default defineConfig({
	extends: [baseConfig, baseJsConfig],
});
