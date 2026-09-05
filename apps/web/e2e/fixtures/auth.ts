import { randomUUID } from 'node:crypto';

import { test as base } from '@playwright/test';
import { createServerClient } from '@supabase/ssr';
import { createClient, type SupabaseClient } from '@supabase/supabase-js';

/**
 * Runs against the kurze-url-preview project, never production. That
 * separation is what makes it acceptable for a service-role key — which
 * bypasses every database policy — to exist as a CI secret at all.
 *
 * Magic-link-only login leaves Playwright nothing to type and no inbox to
 * read, so this mints a session through the Supabase Admin API and sets the
 * resulting cookies directly on the browser context, rather than driving the
 * UI at all.
 *
 * Team creation is the other half of the problem `POST /v1/teams`
 * (apps/api/internal/api/teams.go, `createTeam`) is restricted to the
 * instance maintainers: `d.Config.IsMaintainer(claims.UserID)`, backed by
 * `MAINTAINER_USER_IDS`, a fixed allowlist configured once, at deploy time,
 * on the API's own environment. A user this fixture mints fresh for every
 * single test run can never be on that list — there is no honest way to make
 * a brand-new random user a maintainer without rewriting the preview
 * deployment's configuration on every test run, which would defeat the
 * allowlist's own purpose. So this seeds `team` and `team_member` directly
 * through the service-role PostgREST connection instead of going through the
 * API. That is not a lesser form of authorization than the API's own: there
 * is no RLS anywhere in this schema (CLAUDE.md's golden rule 4 — the Go API
 * itself only ever connects with a service-role connection and enforces
 * tenancy in Go, never in Postgres), so this fixture's direct insert is
 * exactly as authorized as the API's own connection, it just skips the HTTP
 * hop and the maintainer check that exists to gate *real Vereine* asking for
 * a team, not a test seeding its own throwaway one.
 */

interface SessionCookie {
	readonly name: string;
	readonly value: string;
}

/**
 * Mints a real session the same way a browser does after following a magic
 * link, without sending an email or driving any UI: the Admin API's
 * `generateLink` returns a `hashed_token` that `verifyOtp` exchanges directly
 * for an access/refresh token pair. No PKCE code verifier is needed for this
 * — that verifier only guards the *authorization-code* half of the magic
 * link flow (the one a real click on the emailed link would use); token-hash
 * verification is a separate, direct exchange against `/auth/v1/verify`.
 *
 * `admin` is reused for `generateLink` rather than constructing a second
 * client, but `verifyOtp` runs against a dedicated client built with
 * `@supabase/ssr`'s own `createServerClient` and a cookie sink: that is what
 * makes the captured cookies come out chunked and base64url-encoded exactly
 * the way `apps/web/src/server/supabase.ts`'s `createSupabase` (and so the
 * app itself) expects, by running the identical `onAuthStateChange` ->
 * `applyServerStorage` path production uses instead of hand-reproducing its
 * encoding.
 */
async function mintSessionCookies(
	admin: SupabaseClient,
	url: string,
	serviceRoleKey: string,
	email: string,
): Promise<SessionCookie[]> {
	const captured: SessionCookie[] = [];

	const cookieClient = createServerClient(url, serviceRoleKey, {
		cookies: {
			getAll: () => [],
			setAll: (cookies) => {
				for (const cookie of cookies) {
					if (cookie.value !== '') captured.push({ name: cookie.name, value: cookie.value });
				}
			},
		},
	});

	const { data: link, error: linkError } = await admin.auth.admin.generateLink({
		email,
		type: 'magiclink',
	});
	if (linkError) {
		throw new Error(
			`could not generate a magic link for the e2e fixture user: ${linkError.message}`,
		);
	}

	const { error: verifyError } = await cookieClient.auth.verifyOtp({
		token_hash: link.properties.hashed_token,
		type: 'magiclink',
	});
	if (verifyError) {
		throw new Error(
			`could not verify the magic link for the e2e fixture user: ${verifyError.message}`,
		);
	}

	return captured;
}

/**
 * `httpOnly`/`secure`/`sameSite` are set here to match what
 * `SUPABASE_COOKIE_OPTIONS` (`server/supabase.ts`) forces server-side on
 * every write the app itself makes — not copied from whatever options
 * `@supabase/ssr` attached during minting, which this fixture never reads.
 * `secure` still needs to track the scheme: Chromium (Playwright's default)
 * treats `http://localhost` as a secure context, so a `Secure` cookie is
 * still delivered there in local development, but it must not be forced on
 * an `http://` CI preview that isn't `localhost`, or the cookie would never
 * be sent at all.
 */
function toBrowserCookies(
	cookies: readonly SessionCookie[],
	baseURL: string,
): {
	name: string;
	value: string;
	url: string;
	httpOnly: boolean;
	secure: boolean;
	sameSite: 'Lax';
}[] {
	const secure = baseURL.startsWith('https:') || new URL(baseURL).hostname === 'localhost';

	return cookies.map(({ name, value }) => ({
		httpOnly: true,
		name,
		sameSite: 'Lax',
		secure,
		url: baseURL,
		value,
	}));
}

export const test = base.extend<{ teamId: string }>({
	teamId: async ({ context, baseURL }, use): Promise<void> => {
		const url = process.env.SUPABASE_URL;
		const serviceRoleKey = process.env.SUPABASE_SERVICE_ROLE_KEY;
		if (!url || !serviceRoleKey) {
			throw new Error(
				'SUPABASE_URL and SUPABASE_SERVICE_ROLE_KEY are required for authenticated e2e. ' +
					'Without them these specs would run signed out and pass against the login page — ' +
					'the same failure the protection-bypass work fixed in September.',
			);
		}
		// playwright.config.ts always sets `use.baseURL` (to BASE_URL or the
		// localhost fallback), so this is only ever undefined if that invariant
		// is broken — worth a loud failure rather than silently addressing
		// cookies to a wrong host.
		if (!baseURL) throw new Error('baseURL fixture is unset — check playwright.config.ts');

		const admin = createClient(url, serviceRoleKey);
		const email = `e2e-${randomUUID()}@example.com`;

		let userId: string | undefined;
		let teamId: string | undefined;

		try {
			const { data: created, error: createUserError } = await admin.auth.admin.createUser({
				email,
				email_confirm: true,
			});
			// `createUserError`'s falsiness is what narrows `created` to the
			// non-null-`user` branch of `UserResponse`'s discriminated union —
			// there is no case where this passes and `created.user` is still null.
			if (createUserError) {
				throw new Error(`could not create the e2e fixture user: ${createUserError.message}`);
			}
			userId = created.user.id;

			const { data: team, error: teamError } = await admin
				.from('team')
				.insert({ name: `e2e ${randomUUID()}` })
				.select<'id', { id: string }>('id')
				.single();
			// Same discriminated-union narrowing as above: `teamError` falsy means
			// `team` is the non-null success row, never both falsy at once.
			if (teamError) {
				throw new Error(`could not seed the e2e fixture team: ${teamError.message}`);
			}
			teamId = team.id;

			const { error: memberError } = await admin
				.from('team_member')
				.insert({ role: 'owner', team_id: teamId, user_id: userId });
			if (memberError) {
				throw new Error(`could not seed the e2e fixture team membership: ${memberError.message}`);
			}

			const cookies = await mintSessionCookies(admin, url, serviceRoleKey, email);
			await context.addCookies(toBrowserCookies(cookies, baseURL));

			await use(teamId);
		} finally {
			// Team first, then user: `link.created_by` references `auth.users`
			// with no `on delete cascade` (deliberately — see the migration's own
			// comment), so a user deleted first would fail on any link this test
			// created for the team. `team_member`/`link`/`domain`/`tag` all cascade
			// from `team` itself (`on delete cascade`), so deleting the team first
			// leaves nothing behind for the user delete to trip over.
			if (teamId !== undefined) await admin.from('team').delete().eq('id', teamId);
			if (userId !== undefined) await admin.auth.admin.deleteUser(userId);
		}
	},
});
