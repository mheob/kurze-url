import type { Meta, StoryObj } from '@storybook/tanstack-react';
import { expect, userEvent, within } from 'storybook/test';

import { AppSidebar } from './app-sidebar';
import { SidebarProvider } from './ui/sidebar';

const meta = {
	component: AppSidebar,
	// `Sidebar` (Task 4) reads its open/collapsed state off `useSidebar()`, which
	// throws without a `SidebarProvider` ancestor — `AuthedShell` supplies one in
	// the real app (see its own Step 6 composition), so a story rendering
	// `AppSidebar` on its own has to supply the same thing.
	decorators: [
		(Story) => (
			<SidebarProvider>
				<Story />
			</SidebarProvider>
		),
	],
	title: 'Shell/AppSidebar',
} satisfies Meta<typeof AppSidebar>;

export default meta;

export const Default: StoryObj<typeof meta> = {
	args: {
		currentTeamSlug: 'verein-a',
		isMaintainer: false,
		memberships: [{ name: 'Verein A', role: 'owner', slug: 'verein-a', team_id: 'a' }],
		onSignOut: () => undefined,
		signingOut: false,
		theme: 'light',
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dark palette is never
// checked by anything, only viewable by hand. `args.theme` is what ThemeToggle
// renders from; `globals.theme` is what preview.tsx's decorator reads to add
// the `dark` class. Both are needed, or the story is half dark.
export const Dark: StoryObj<typeof meta> = {
	args: { ...Default.args, theme: 'dark' },
	globals: { theme: 'dark' },
};

// "Sign out everywhere" is the sidebar's own state, so no args can reach the ticked box: a `play`
// function that clicks it is what lets the a11y addon audit the box in its checked state too, not
// only the unchecked default above. The assertion also pins that the click lands on the box and
// toggles it, which is what the label's `htmlFor` wiring is for.
export const SignOutEverywhere: StoryObj<typeof meta> = {
	args: Default.args,
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- Storybook's own `play` function context type; not this codebase's to mark readonly.
	play: async ({ canvasElement }) => {
		const canvas = within(canvasElement);
		await userEvent.click(canvas.getByRole('checkbox', { name: 'Sign out everywhere' }));
		await expect(canvas.getByRole('checkbox', { name: 'Sign out everywhere' })).toBeChecked();
	},
};
