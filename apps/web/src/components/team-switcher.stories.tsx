import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { TeamSwitcher } from './team-switcher';

const meta = {
	component: TeamSwitcher,
	title: 'Shell/TeamSwitcher',
} satisfies Meta<typeof TeamSwitcher>;

export default meta;

export const TwoTeams: StoryObj<typeof meta> = {
	args: {
		currentTeamSlug: 'tsg-irlich',
		memberships: [
			{ name: 'TSG Irlich', role: 'owner', slug: 'tsg-irlich', team_id: 'a' },
			{ name: 'SV Beispiel', role: 'editor', slug: 'sv-beispiel', team_id: 'b' },
		],
	},
};

// The theme toolbar global defaults to `light`, and `test:storybook` runs every
// story at its defaults — so without this story the dropdown's dark palette is
// never checked by anything, only viewable by hand. `globals.theme` is what
// preview.tsx's decorator reads to add the `dark` class; `TeamSwitcher` itself
// takes no `theme` prop, so unlike `AppSidebar`'s pair only `globals` differs
// here.
export const Dark: StoryObj<typeof meta> = {
	args: { ...TwoTeams.args },
	globals: { theme: 'dark' },
};
