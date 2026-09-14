import { useTranslation } from 'react-i18next';

import type { Theme } from '../lib/preferences';
import type { Membership } from '../routes/_authed';
import { AppSidebar } from './app-sidebar';
import { SidebarTrigger } from './sidebar-trigger';
import { Separator } from './ui/separator';
import { SidebarInset, SidebarProvider } from './ui/sidebar';

interface AuthedShellProps {
	readonly children: React.ReactNode;
	readonly currentTeamSlug: string | undefined;
	// Whether to offer team creation at all. `/` covers the maintainer who has
	// no team yet; this covers the one who does, and who would otherwise have
	// no way back to `/new-team` from inside the app.
	readonly isMaintainer: boolean;
	readonly memberships: readonly Membership[];
	readonly onSignOut: () => void;
	readonly signingOut: boolean;
	// `AppSidebar`'s footer needs this for its `ThemeToggle`; see that
	// component's own docstring for why it arrives as a prop rather than a
	// `usePreferences()` call inside either component.
	readonly theme: Theme;
}

/**
 * The chrome every authenticated page shares: a real sidebar (`AppSidebar`)
 * plus the slim top bar that hosts its trigger, wrapping `children` — the
 * page content each route renders through `SidebarInset`. Presentational and
 * prop-driven, the same idiom as `LinkList`/`LinkForm`: `_authed.tsx`'s route
 * component owns the router/mutation wiring (resolving the current team slug
 * from the URL, calling the `signOut` server function, reading `theme` off
 * `usePreferences()`) and passes plain data, a callback and the matched child
 * route in here, so this can be rendered and tested without a `QueryClient`,
 * a router, or a real session.
 *
 * `children` exists because `SidebarInset` — the sidebar's own content
 * column — has to wrap the page content for the layout to work; before this
 * task, `AuthedShell` only ever rendered its own header and `_authed.tsx`
 * rendered `<Outlet>` as a sibling.
 *
 * @param props - The component's props.
 * @param props.children - The matched child route's content, rendered inside `SidebarInset`.
 * @param props.currentTeamSlug - The resolved current team's slug, or undefined when there is none (e.g. a maintainer with no team yet, on `/new-team`).
 * @param props.isMaintainer - Whether to offer team creation.
 * @param props.memberships - The signed-in visitor's team memberships.
 * @param props.onSignOut - Called when the sign-out control is clicked.
 * @param props.signingOut - True while sign-out is in flight; disables the sign-out control.
 * @param props.theme - The current theme preference; passed straight through to `AppSidebar`.
 * @returns The rendered authenticated shell.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactNode` is React's own type; not a declaration this file can edit.
export function AuthedShell({
	children,
	currentTeamSlug,
	isMaintainer,
	memberships,
	onSignOut,
	signingOut,
	theme,
}: AuthedShellProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<SidebarProvider>
			<AppSidebar
				currentTeamSlug={currentTeamSlug}
				isMaintainer={isMaintainer}
				memberships={memberships}
				onSignOut={onSignOut}
				signingOut={signingOut}
				theme={theme}
			/>
			<SidebarInset>
				<header className="flex h-12 items-center gap-2 border-b px-4">
					<SidebarTrigger />
					{/* oxlint-disable-next-line react/forbid-component-props -- `Separator` (components/ui/separator.tsx) forwards `className` straight to its underlying element; this is how every caller sizes and orients it, the same as `orientation` below. */}
					<Separator className="h-4" orientation="vertical" />
					<span className="font-semibold">{t('brand')}</span>
				</header>
				{children}
			</SidebarInset>
		</SidebarProvider>
	);
}
