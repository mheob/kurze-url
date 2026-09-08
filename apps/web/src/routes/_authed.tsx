import { getMe } from '@kurze-url/api-client';
import { useMutation } from '@tanstack/react-query';
import {
	createFileRoute,
	notFound,
	Outlet,
	redirect,
	useParams,
	useRouter,
} from '@tanstack/react-router';
import { createServerFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { AuthedShell } from '../components/authed-shell';
import { classifyApiError } from '../lib/api-errors';
import { signOut } from '../server/auth';
import {
	authedApiClient,
	flushSessionCookies,
	isUnauthenticatedError,
	requireSession,
} from '../server/session';

// oxlint's typescript(consistent-type-definitions) is error-level (see the
// same deviation note in server/health.ts): an object shape needs an
// `interface`, a `type` alias is rejected.
export interface Membership {
	name: string;
	role: string;
	slug: string;
	team_id: string;
}

export interface Me {
	email: string;
	// Mirrors the check `POST /v1/teams` enforces. Nothing in the browser can
	// derive it — `MAINTAINER_USER_IDS` is deploy-time configuration on the Go
	// service — so the API reports it and the routes below gate on it rather
	// than offering team creation to everyone and letting a 403 arrive after
	// the form is filled in. See `MeOutput` in `apps/api/internal/api/me.go`.
	is_maintainer: boolean;
	memberships: Membership[];
	user_id: string;
}

/**
 * Reads a session — via `requireSession`, which calls through to
 * `@supabase/ssr`'s `getSession` — and that read is itself what refreshes an
 * expiring session, writing new cookies into the `Headers` object threaded
 * through. `flushSessionCookies` is what carries those onto the real
 * response; skipping it here would reproduce, for every authenticated page
 * load, the exact "login that works and then silently stops" failure Tasks
 * 4 and 5 already hit (and fixed) for sign-in and sign-out.
 *
 * Logic lives inline in the `.handler()` closure — `signOut`'s shape in
 * `server/auth.ts`, not `sendMagicLinkFor`'s separately-exported-and-wrapped
 * one. That second shape exists only for logic that needs testing
 * independent of a request (`sendMagicLinkFor`'s enumeration-timing
 * guarantee); nothing here does — the only thing this task's brief asks to
 * be unit-tested is the pure `requireTeamId` below. Extracting this body
 * into a named helper would need `createServerOnlyFn` to keep the client
 * bundle buildable (see `sendMagicLinkFor`'s docstring for why); left inline,
 * it doesn't.
 *
 * `memberships` is normalized from the generated client's
 * `Array<TeamMembership> | null` (Huma serialises a nil Go slice as JSON
 * `null`) to a plain array: `requireTeamId` below and every later
 * consumer of `Me` are written against `Membership[]`, so the `?? []`
 * happens once, here, instead of once per call site.
 */
export const fetchMe = createServerFn({ method: 'GET' }).handler(async (): Promise<Me> => {
	const headers = new Headers();
	const { accessToken } = await requireSession(getRequest(), headers);
	flushSessionCookies(headers);

	const { data } = await getMe({ client: authedApiClient(accessToken), throwOnError: true });
	return {
		email: data.email,
		is_maintainer: data.is_maintainer,
		memberships: data.memberships ?? [],
		user_id: data.user_id,
	};
});

/**
 * 404, never 403: `internal/authz` in the Go API already answers a non-member
 * with 404, never 403, so the API itself never confirms that a team exists at
 * all. A frontend that rendered "forbidden" here would leak exactly the
 * information the API withholds — so this throws the router's own `notFound()`,
 * and the test file asserts on that distinction with `isNotFound` rather than a
 * bare `.toThrow()`.
 *
 * It returns the team id rather than only asserting, because the URL now
 * carries the slug while every API call still takes the UUID. Both questions —
 * "may this caller be here" and "which team is this" — are answered by one
 * lookup in the membership list `_authed`'s `beforeLoad` has already fetched,
 * so nothing here costs a request.
 */
export function requireTeamId(memberships: Membership[], teamSlug: string): string {
	const membership = memberships.find((entry) => entry.slug === teamSlug);
	if (!membership) throw notFound();
	return membership.team_id;
}

export const Route = createFileRoute('/_authed')({
	beforeLoad: async () => {
		try {
			return { me: await fetchMe() };
		} catch (error) {
			if (isUnauthenticatedError(error)) throw redirect({ to: '/login' });
			throw error;
		}
	},
	component: AuthedLayout,
});

/**
 * Wires `AuthedShell` (Finding 2) into the actual route tree: `TeamSwitcher`
 * and the sign-out control were both built, tested and (for the switcher)
 * storied in earlier tasks, but nothing rendered either one until now.
 *
 * `useParams({ strict: false })` (not `Route.useParams()`, which only sees
 * this route's *own* params — `_authed` is a pathless layout with none) is
 * what reaches `teamSlug` from whichever child route is actually matched;
 * falling back to the first membership covers the layout rendering above a
 * child that has no `teamSlug` of its own, or none at all (see `AuthedShell`'s
 * own docstring for why `currentTeamSlug` is optional rather than assumed).
 */
function AuthedLayout(): React.JSX.Element {
	const { me } = Route.useRouteContext();
	const { t } = useTranslation();
	const router = useRouter();
	const { teamSlug } = useParams({ strict: false });
	const [signOutFailed, setSignOutFailed] = useState(false);

	const signOutMutation = useMutation({
		mutationFn: () => signOut(),
		onError: (error: unknown) => {
			// Already signed out from the API's point of view — same "nothing
			// left to undo" reasoning as any other `unauthenticated` classification
			// elsewhere in this app — so this still lands on `/login`, just without
			// pretending the click failed.
			if (classifyApiError(error).kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setSignOutFailed(true);
		},
		onSuccess: async () => {
			setSignOutFailed(false);
			await router.navigate({ to: '/' });
		},
	});

	return (
		<>
			<AuthedShell
				currentTeamSlug={teamSlug ?? me.memberships[0]?.slug}
				isMaintainer={me.is_maintainer}
				memberships={me.memberships}
				onSignOut={() => signOutMutation.mutate()}
				signingOut={signOutMutation.isPending}
			/>
			{/* Every authenticated page renders through this one `<Outlet>`, so
			    the `<main>` belongs here rather than in each child route: axe's
			    `landmark-one-main` wants exactly one per document, and a per-page
			    wrapper would either duplicate it or be forgotten on the next
			    route added. The sign-out failure goes inside it too — `region`
			    fails any content that sits in no landmark at all, and this
			    `role="alert"` is the only shell-level node that isn't part of
			    `AuthedShell`'s own `<header>` banner. */}
			<main>
				{signOutFailed ? <p role="alert">{t('errors.unknown')}</p> : null}
				<Outlet />
			</main>
		</>
	);
}
