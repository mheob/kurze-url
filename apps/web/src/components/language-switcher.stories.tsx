import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { LanguageSwitcher } from './language-switcher';

const meta = {
	component: LanguageSwitcher,
	title: 'Shell/LanguageSwitcher',
} satisfies Meta<typeof LanguageSwitcher>;

export default meta;

export const Default: StoryObj<typeof meta> = {};
