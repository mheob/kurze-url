import { Link } from '@tanstack/react-router';
import { GlobeIcon, LinkIcon } from 'lucide-react';
import { useTranslation } from 'react-i18next';

import type { Theme } from '../lib/preferences';
import type { Membership } from '../routes/_authed';
import { LanguageSwitcher } from './language-switcher';
import { TeamSwitcher } from './team-switcher';
import { ThemeToggle } from './theme-toggle';
import { Button, buttonVariants } from './ui/button';
import {
	Sidebar,
	SidebarContent,
	SidebarFooter,
	SidebarHeader,
	SidebarMenu,
	SidebarMenuButton,
	SidebarMenuItem,
} from './ui/sidebar';

interface AppSidebarProps {
	readonly currentTeamSlug: string | undefined;
	// Whether to offer team creation at all. `/` covers the maintainer who has
	// no team yet; this covers the one who does, and who would otherwise have
	// no way back to `/new-team` from inside the app.
	readonly isMaintainer: boolean;
	readonly memberships: readonly Membership[];
	readonly onSignOut: () => void;
	readonly signingOut: boolean;
	// `ThemeToggle`'s footer control needs this; `AuthedShell` sources it from
	// `usePreferences()` and threads it down, rather than this component
	// calling the hook itself — see this file's own docstring for why.
	readonly theme: Theme;
}

/**
 * The chrome every authenticated page shares, now a real sidebar rather than
 * a flat header. Presentational and prop-driven, the same idiom as
 * `LinkList`/`LinkForm`: `AuthedShell` (via `_authed.tsx`) owns the
 * router/mutation wiring — resolving the current team slug from the URL,
 * calling the `signOut` server function, reading `theme` off
 * `usePreferences()` — and passes plain data and a callback in here, so this
 * can be rendered and tested without a `QueryClient`, a router, or a real
 * session. `theme` is a plain prop rather than a `usePreferences()` call
 * inside this component for the same reason: calling the hook in here would
 * make this component unrenderable without a router in context, which is
 * exactly the property this task exists to preserve.
 *
 * `currentTeamSlug` is optional, not read off `memberships[0]` in here: a
 * signed-in visitor with zero memberships can still reach this shell (e.g.
 * `/`'s `noTeam` outcome never enters `_authed` at all, but a stale bookmark
 * to a team the visitor has since left 404s deeper in the tree, past this
 * shell) and `TeamSwitcher` has nothing to switch between in that case.
 *
 * The Links/Domains section list shares `TeamSwitcher`'s exact guard —
 * `currentTeamSlug !== undefined && memberships.length > 0` — for the same
 * reason: with no resolved team, or a `memberships` list that doesn't
 * actually contain it, there is nowhere for either link to point.
 *
 * `ThemeToggle` and `LanguageSwitcher` are rendered here, in the footer, for
 * the first time in the authenticated area — before this task, both were
 * only ever rendered by `SiteHeader` on the public pages, so a signed-in
 * visitor had no way to switch either at all.
 *
 * @param props - The component's props.
 * @param props.currentTeamSlug - The resolved current team's slug, or undefined when there is none (e.g. a stale bookmark to a team the visitor has left).
 * @param props.isMaintainer - Whether to offer team creation.
 * @param props.memberships - The signed-in visitor's team memberships.
 * @param props.onSignOut - Called when the sign-out control is clicked.
 * @param props.signingOut - True while sign-out is in flight; disables the sign-out control.
 * @param props.theme - The current theme preference; passed straight through to the footer's `ThemeToggle`.
 * @returns The rendered sidebar.
 */
export function AppSidebar({
	currentTeamSlug,
	isMaintainer,
	memberships,
	onSignOut,
	signingOut,
	theme,
}: AppSidebarProps): React.JSX.Element {
	const { t } = useTranslation();
	const hasResolvedTeam = currentTeamSlug !== undefined && memberships.length > 0;

	return (
		<Sidebar>
			<SidebarHeader>
				{hasResolvedTeam ? (
					<TeamSwitcher currentTeamSlug={currentTeamSlug} memberships={memberships} />
				) : null}
			</SidebarHeader>
			<SidebarContent>
				{hasResolvedTeam ? (
					// `SidebarMenu` (components/ui/sidebar.tsx) renders a plain `<ul>` —
					// an `aria-label` on a list is not a landmark, so the accessible
					// name has to live on a real `<nav>` wrapping it, the same
					// "real element over a bolted-on attribute" preference
					// `team-switcher.tsx`'s `<fieldset>` follows for its own label.
					<nav aria-label={t('nav.label')}>
						<SidebarMenu>
							<SidebarMenuItem>
								<SidebarMenuButton
									render={
										// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- Base UI's `render`-prop composition idiom (`useRender`'s "Migrating from Radix UI" guide): this is the element `SidebarMenuButton` clones and merges its own props onto, the same pattern `components/ui/sheet.tsx`'s generated `SheetClose` uses. A stable reference would need a `useMemo` around a two-line static element in a two-item menu.
										<Link params={{ teamSlug: currentTeamSlug }} to="/teams/$teamSlug/links" />
									}
								>
									<LinkIcon aria-hidden />
									<span>{t('nav.links')}</span>
								</SidebarMenuButton>
							</SidebarMenuItem>
							<SidebarMenuItem>
								<SidebarMenuButton
									render={
										// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- same reason as the `links` button above.
										<Link params={{ teamSlug: currentTeamSlug }} to="/teams/$teamSlug/domains" />
									}
								>
									<GlobeIcon aria-hidden />
									<span>{t('nav.domains')}</span>
								</SidebarMenuButton>
							</SidebarMenuItem>
						</SidebarMenu>
					</nav>
				) : null}
			</SidebarContent>
			<SidebarFooter>
				<div className="flex items-center justify-between gap-2 p-1">
					<LanguageSwitcher />
					<ThemeToggle theme={theme} />
				</div>
				{isMaintainer ? (
					// oxlint-disable-next-line react/forbid-component-props -- shadcn/ui's own "link styled as a button" idiom: TanStack Router's `Link` forwards `className` straight to the rendered `<a>`, and `buttonVariants` exists precisely to be applied here.
					<Link className={buttonVariants({ size: 'sm', variant: 'ghost' })} to="/new-team">
						{t('teams.create')}
					</Link>
				) : null}
				<Button disabled={signingOut} onClick={onSignOut} type="button" variant="outline">
					{t('auth.signOut')}
				</Button>
			</SidebarFooter>
		</Sidebar>
	);
}
