import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { teamCookie } from '../lib/current-team';
import type { Membership } from '../routes/_authed';

interface TeamSwitcherProps {
	readonly currentTeamId: string;
	readonly memberships: readonly Membership[];
}

/**
 * Written client-side on click, not server-side on navigation: switching
 * teams is a navigation, not a mutation, so there is no response here to
 * attach a `Set-Cookie` to. The server reads the cookie on the next request
 * that lands on `/` — the same read `resolveCurrentTeam` already does.
 *
 * Module scope, not inside the component: it captures nothing from a
 * component closure, and defining it inline reads to the linter as a
 * render-time mutation of `document` rather than the click-time one it
 * actually is (the same reasoning `language-switcher.tsx`'s `choose` is
 * factored out for).
 */
function remember(teamId: string): void {
	document.cookie = teamCookie(teamId);
}

export function TeamSwitcher({ currentTeamId, memberships }: TeamSwitcherProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<nav aria-label={t('teams.switcherLabel')}>
			<ul>
				{memberships.map((membership) => (
					<li key={membership.team_id}>
						<Link
							aria-current={membership.team_id === currentTeamId ? 'page' : undefined}
							onClick={() => remember(membership.team_id)}
							params={{ teamId: membership.team_id }}
							to="/teams/$teamId/links"
						>
							{membership.name}
						</Link>
					</li>
				))}
			</ul>
		</nav>
	);
}
