import type { Meta, StoryObj } from '@storybook/tanstack-react';

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
