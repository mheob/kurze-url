import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { LoginForm } from './login';

/**
 * `LoginForm` calls the `sendMagicLink` server function directly (it isn't
 * prop-driven the way `LinkForm`'s `onSubmit` is), so the only state a story
 * can reach without standing up a real server is this one: the idle form
 * before anything is submitted. That is also the state every visitor to
 * `/login` actually lands on first — the two screens Finding 6 calls out,
 * `LinkList` and this one, are exactly the screens everyone sees.
 */
const meta = {
	component: LoginForm,
	title: 'Auth/LoginForm',
} satisfies Meta<typeof LoginForm>;

export default meta;

export const Default: StoryObj<typeof meta> = {};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand.
export const Dark: StoryObj<typeof meta> = {
	globals: { theme: 'dark' },
};
