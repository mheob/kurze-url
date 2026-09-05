import { createFileRoute, Link, redirect } from '@tanstack/react-router';
import { createServerFn } from '@tanstack/react-start';
import { getRequestHeader } from '@tanstack/react-start/server';
import { useTranslation } from 'react-i18next';

import { SiteFooter } from '../components/site-footer';
import { SiteHeader } from '../components/site-header';
import { buttonVariants } from '../components/ui/button';
import { resolveCurrentTeam } from '../lib/current-team';
import { usePreferences } from '../lib/use-preferences';
import { fetchHealth } from '../server/health';
import { isUnauthenticatedError } from '../server/session';
import { fetchMe, type Me, type Membership } from './_authed';

/**
 * Wraps fetchHealth in a server function so the loader — isomorphic, like
 * __root.tsx's getPreferences — never runs the API call in the browser. On
 * the initial SSR pass this executes in-process; on a client-side navigation
 * back to this route it becomes an RPC to this app's own server, which is the
 * only thing the browser ever talks to.
 */
const getHealthStatus = createServerFn({ method: 'GET' }).handler(() => fetchHealth());

/**
 * Resolves the remembered team id inside a server function for the same
 * reason `__root.tsx`'s `getPreferences` reads `lang`/`theme` inside one:
 * `getRequestHeader` only works inside the server's per-request context, so
 * a plain call would throw the moment this loader re-runs client-side on a
 * navigation back to `/`.
 *
 * The resolution itself — not just the header read — stays inside this
 * function; only the resolved team id (already visible to the client as
 * one of `me.memberships`' own ids, so no new information) crosses back out.
 * The Supabase session cookie in `server/supabase.ts` is deliberately
 * `httpOnly` so client JS can never read it; returning the raw `Cookie`
 * header from a server function would hand an RPC response — readable by
 * any script on the page — exactly the value `httpOnly` exists to keep from
 * it, on every client-side navigation back to this route. Doing the
 * `resolveCurrentTeam` call in here instead of at the loader call site is
 * what avoids that.
 */
const getCurrentTeamId = createServerFn({ method: 'GET' })
	.validator((memberships: Membership[]) => memberships)
	.handler(({ data: memberships }) => resolveCurrentTeam(getRequestHeader('cookie'), memberships));

/**
 * `/` is public, so "no session" is the ordinary case here, not a failure —
 * unlike `_authed.tsx`'s `beforeLoad`, which treats the same
 * `UnauthenticatedError` as a reason to redirect to `/login`. `fetchMe`
 * (defined in `_authed.tsx`, reused rather than duplicated: it is already
 * the one place that reads a session, normalises `memberships`, and — via
 * `requireSession` — flushes the refreshed session cookie onto the real
 * response) throws that error when there is none; `isUnauthenticatedError`
 * is what tells that apart from every other failure, and survives the same
 * cross-boundary reconstruction on a client-side navigation that its
 * docstring in `server/session.ts` explains. Anything else — an actual
 * failure reading `/v1/me` — is rethrown rather than swallowed: this route
 * only has an opinion about the signed-out case.
 */
export async function fetchCurrentUser(): Promise<Me | undefined> {
	try {
		return await fetchMe();
	} catch (error) {
		if (isUnauthenticatedError(error)) return undefined;
		throw error;
	}
}

// oxlint's typescript(consistent-type-definitions) is error-level (see the
// deviation note in server/health.ts) for a `type` alias naming a single
// object shape — it does not apply to a discriminated union, which cannot
// be written as an `interface` at all. `lib/preferences.ts`'s `Language`/
// `Theme` are the same kind of exception.
export type HomeOutcome =
	| { kind: 'marketing' }
	| { kind: 'noTeam' }
	| { kind: 'redirect'; teamId: string };

/**
 * The three cases `/` owns. `_authed/index.tsx` was dropped from the plan:
 * a pathless layout's own index file resolves to the same full path ("/")
 * as this route, which `@tanstack/router-generator` rejects as a duplicate
 * — see "Fix round 1" in task-6-report.md. Its "redirect a signed-in
 * visitor to their team" job moved here instead, onto the route that
 * actually owns `/`.
 *
 * `teamId` arrives already resolved — by `getCurrentTeamId`, which wraps
 * `resolveCurrentTeam` around the request's `team` cookie and this visitor's
 * memberships — rather than being picked here. That keeps this function a
 * plain, three-way decision with no cookie or membership-list logic of its
 * own, and it's what makes a returning visitor land back on the team they
 * last used instead of always the first one in membership order.
 */
export function resolveHomeOutcome(me: Me | undefined, teamId: string | undefined): HomeOutcome {
	if (!me) return { kind: 'marketing' };

	return teamId ? { kind: 'redirect', teamId } : { kind: 'noTeam' };
}

export const Route = createFileRoute('/')({
	component: Home,
	loader: async () => {
		const [health, me] = await Promise.all([getHealthStatus(), fetchCurrentUser()]);
		const teamId = me ? await getCurrentTeamId({ data: me.memberships }) : undefined;
		const outcome = resolveHomeOutcome(me, teamId);
		if (outcome.kind === 'redirect') {
			throw redirect({ params: { teamId: outcome.teamId }, to: '/teams/$teamId/links' });
		}
		return { outcome, status: health.status };
	},
});

function Home() {
	const { t } = useTranslation();
	const { theme } = usePreferences();
	const { outcome, status } = Route.useLoaderData();

	return (
		<div className="bg-background text-foreground flex min-h-screen flex-col">
			<SiteHeader theme={theme} />
			<main className="flex flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
				{outcome.kind === 'noTeam' ? (
					<p className="text-muted-foreground max-w-prose">{t('teams.none')}</p>
				) : (
					<>
						<h1 className="text-3xl font-bold">{t('home.heading')}</h1>
						<p className="text-muted-foreground max-w-prose">{t('home.body')}</p>
						<Link className={buttonVariants({ variant: 'default' })} to="/login">
							{t('actions.signIn')}
						</Link>
					</>
				)}
			</main>
			<SiteFooter apiStatus={status} />
		</div>
	);
}
