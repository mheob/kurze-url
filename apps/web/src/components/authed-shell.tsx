import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import type { Membership } from '../routes/_authed';
import { TeamSwitcher } from './team-switcher';
import { Button } from './ui/button';

interface AuthedShellProps {
	readonly currentTeamId: string | undefined;
	// Whether to offer team creation at all. `/` covers the maintainer who has
	// no team yet; this covers the one who does, and who would otherwise have
	// no way back to `/teams/new` from inside the app.
	readonly isMaintainer: boolean;
	readonly memberships: readonly Membership[];
	readonly onSignOut: () => void;
	readonly signingOut: boolean;
}

/**
 * The chrome every authenticated page shares. `TeamSwitcher` was built,
 * tested and storied in an earlier task but never rendered anywhere — same
 * for `auth.signOut`, which had no caller at all (Finding 2). Presentational
 * and prop-driven, the same idiom as `LinkList`/`LinkForm`: `_authed.tsx`'s
 * route component owns the router/mutation wiring (resolving the current team
 * id from the URL, calling the `signOut` server function) and passes plain
 * data and a callback in here, so this can be rendered and tested without a
 * `QueryClient`, a router, or a real session.
 *
 * `currentTeamId` is optional, not read off `memberships[0]` in here: a
 * signed-in visitor with zero memberships can still reach this shell (e.g.
 * `/`'s `noTeam` outcome never enters `_authed` at all, but a stale bookmark
 * to a team the visitor has since left 404s deeper in the tree, past this
 * shell) and `TeamSwitcher` has nothing to switch between in that case.
 */
export function AuthedShell({
	currentTeamId,
	isMaintainer,
	memberships,
	onSignOut,
	signingOut,
}: AuthedShellProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<header className="border-border flex items-center justify-between border-b px-6 py-4">
			{currentTeamId && memberships.length > 0 ? (
				<TeamSwitcher currentTeamId={currentTeamId} memberships={memberships} />
			) : null}
			<div className="flex items-center gap-2">
				{isMaintainer ? <Link to="/teams/new">{t('teams.create')}</Link> : null}
				<Button disabled={signingOut} onClick={onSignOut} type="button" variant="outline">
					{t('auth.signOut')}
				</Button>
			</div>
		</header>
	);
}
