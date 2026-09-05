import { getMe } from '@kurze-url/api-client';
import { createFileRoute, notFound, Outlet, redirect } from '@tanstack/react-router';
import { createServerFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import {
	authedApiClient,
	flushSessionCookies,
	requireSession,
	UnauthenticatedError,
} from '../server/session';

// oxlint's typescript(consistent-type-definitions) is error-level (see the
// same deviation note in server/health.ts): an object shape needs an
// `interface`, a `type` alias is rejected.
export interface Membership {
	name: string;
	role: string;
	team_id: string;
}

export interface Me {
	email: string;
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
 * be unit-tested is the pure `assertMembership` below. Extracting this body
 * into a named helper would need `createServerOnlyFn` to keep the client
 * bundle buildable (see `sendMagicLinkFor`'s docstring for why); left inline,
 * it doesn't.
 *
 * `memberships` is normalized from the generated client's
 * `Array<TeamMembership> | null` (Huma serialises a nil Go slice as JSON
 * `null`) to a plain array: `assertMembership` below and every later
 * consumer of `Me` are written against `Membership[]`, so the `?? []`
 * happens once, here, instead of once per call site.
 */
export const fetchMe = createServerFn({ method: 'GET' }).handler(async (): Promise<Me> => {
	const headers = new Headers();
	const { accessToken } = await requireSession(getRequest(), headers);
	flushSessionCookies(headers);

	const { data } = await getMe({ client: authedApiClient(accessToken), throwOnError: true });
	return { email: data.email, memberships: data.memberships ?? [], user_id: data.user_id };
});

/**
 * 404, never 403: `internal/authz` in the Go API already answers a
 * non-member with 404, never 403, so the API itself never confirms that a
 * team exists at all. A frontend that rendered "forbidden" here would leak
 * exactly the information the API withholds — so this throws the router's
 * own `notFound()`, not a generic error, and the test file asserts on that
 * distinction with `isNotFound` rather than a bare `.toThrow()`.
 */
export function assertMembership(memberships: Membership[], teamId: string): void {
	if (!memberships.some((membership) => membership.team_id === teamId)) throw notFound();
}

/**
 * Not a plain `error instanceof UnauthenticatedError`, deliberately: that
 * check is only reliable for the very first render of an `_authed` route,
 * where `beforeLoad` runs on the server and `fetchMe()` calls its handler
 * in-process — the real class instance comes straight back. Every later
 * client-side navigation into an `_authed` route runs `beforeLoad` again, in
 * the browser this time, where `fetchMe()` is a genuine HTTP round trip. A
 * thrown error crossing that boundary is serialised with `seroval`
 * (`@tanstack/start-server-core`'s `server-functions-handler.js` calls
 * `toCrossJSONAsync` on it) and reconstructed with `fromCrossJSON` on the
 * other side. seroval only special-cases the built-in `Error` subclasses
 * (`TypeError`, `RangeError`, …its own `ERROR_CONSTRUCTOR` table) — an
 * application-defined one like `UnauthenticatedError` comes back as a plain
 * `Error` with `.name` restored as an own property, not as an instance of
 * the original class. Confirmed by reading the installed `seroval@1.6.4`'s
 * `deserializeError`/`getInitialErrorOptions`. Without the `.name` check
 * here, a session that expires while the app is already open would fail to
 * redirect to `/login` on the next in-app navigation and would instead
 * surface a raw error — silently, the same family of failure the cookie
 * conventions this plan already documents warn about, just on the read side
 * rather than the write side. `UnauthenticatedError.name` (the class's own
 * name, not an instance's) is used rather than the string literal so a
 * rename of the class can't quietly desync this check from it.
 */
function isUnauthenticatedError(error: unknown): boolean {
	return error instanceof Error && error.name === UnauthenticatedError.name;
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
	component: () => <Outlet />,
});
