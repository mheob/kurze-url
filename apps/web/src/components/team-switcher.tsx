import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { teamCookie } from '../lib/current-team';
import type { Membership } from '../routes/_authed';
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from './ui/dropdown-menu';

interface TeamSwitcherProps {
	readonly currentTeamSlug: string;
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
 *
 * @param teamSlug - The team's slug to remember; read back on the next request to `/`.
 */
function remember(teamSlug: string): void {
	// The Cookie Store API's `set()` is Promise-based; this write has to be
	// visible to the very next request to `/`, which can follow this click
	// synchronously, so an awaited alternative could lose the race.
	// oxlint-disable-next-line unicorn/no-document-cookie
	document.cookie = teamCookie(teamSlug);
}

export function TeamSwitcher({
	currentTeamSlug,
	memberships,
}: TeamSwitcherProps): React.JSX.Element {
	const { t } = useTranslation();
	const currentTeam = memberships.find((membership) => membership.slug === currentTeamSlug);

	return (
		// A `<fieldset>` carries the implicit ARIA role "group" natively, the same
		// idiom `language-switcher.tsx` uses for the same reason: a real semantic
		// element satisfies both the a11y preference for one over a bolted-on
		// `role` attribute and the test's `getByRole('group')` query. The trigger's
		// own accessible name is the current team's name, not this label — the two
		// serve different questions ("which control switches teams" vs. "which team
		// is current").
		<fieldset aria-label={t('teams.switcherLabel')}>
			<DropdownMenu>
				{/* oxlint-disable-next-line react/forbid-component-props -- `DropdownMenuTrigger` (components/ui/dropdown-menu.tsx) forwards `className` straight to the rendered `<button>`; this is how every caller styles it. */}
				<DropdownMenuTrigger className="flex w-full items-center justify-between gap-2 truncate rounded-none border border-transparent px-3 py-2 text-left text-sm font-medium hover:bg-sidebar-accent hover:text-sidebar-accent-foreground">
					<span className="truncate">{currentTeam?.name}</span>
				</DropdownMenuTrigger>
				<DropdownMenuContent>
					{memberships.map((membership) => (
						<DropdownMenuItem
							aria-current={membership.slug === currentTeamSlug ? 'page' : undefined}
							key={membership.team_id}
							onClick={() => {
								remember(membership.slug);
							}}
							// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- Base UI's `render`-prop composition idiom (`useRender`'s "Migrating from Radix UI" guide): this is the element `DropdownMenuItem` clones and merges its own props onto. A stable reference would need a `useMemo` around a two-line static element per row of a short team list.
							render={<Link params={{ teamSlug: membership.slug }} to="/teams/$teamSlug/links" />}
						>
							{membership.name}
						</DropdownMenuItem>
					))}
				</DropdownMenuContent>
			</DropdownMenu>
		</fieldset>
	);
}
