import type { Meta, StoryObj } from '@storybook/tanstack-react';

import { TeamSwitcher } from './team-switcher';

const meta = {
	component: TeamSwitcher,
	title: 'Shell/TeamSwitcher',
} satisfies Meta<typeof TeamSwitcher>;

export default meta;

export const TwoTeams: StoryObj<typeof meta> = {
	args: {
		currentTeamId: 'a',
		memberships: [
			{ name: 'TSG Irlich', role: 'owner', team_id: 'a' },
			{ name: 'SV Beispiel', role: 'editor', team_id: 'b' },
		],
	},
};
