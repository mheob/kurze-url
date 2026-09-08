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
