import type { StorybookConfig } from '@storybook/tanstack-react';

const config: StorybookConfig = {
	addons: ['@storybook/addon-a11y'],
	// This instance is never deployed and CI builds it on every push — no
	// point phoning usage data home for a build nobody visits.
	core: { disableTelemetry: true },
	framework: '@storybook/tanstack-react',
	stories: ['../src/**/*.stories.@(js|jsx|mjs|ts|tsx)'],
};

export default config;
