import { randomUUID } from 'node:crypto';

import { test as base } from '@playwright/test';
import { createServerClient } from '@supabase/ssr';
import { createClient, type SupabaseClient } from '@supabase/supabase-js';
import { Client as PgClient } from 'pg';

/**
 * Runs against the kurze-url-preview project, never production. That
 * separation is what makes it acceptable for a service-role key — which
 * bypasses every database policy — and a direct database connection string
 * to exist as CI secrets at all.
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
 * allowlist's own purpose. So this seeds `team` and `team_member` directly,
 * over a plain Postgres connection (`E2E_DATABASE_URL`, `node-postgres`),
 * instead of going through the API.
 *
 * That connection is deliberately not PostgREST (`supabase-js`'s
 * `.from(...)`, i.e. `/rest/v1/`): this product never talks to PostgREST
 * anywhere else — the Go API itself connects to Postgres directly with pgx —
 * so a test fixture routing through it would couple the e2e suite to a
 * service that isn't part of the system, and PostgREST outages (schema-cache
 * failures, in particular) have already broken this suite for reasons that
 * have nothing to do with the app. A direct connection is also not a lesser
 * form of authorization than the API's own: there is no RLS anywhere in this
 * schema (CLAUDE.md's golden rule 4 — the Go API itself only ever connects
 * with a service-role-equivalent connection and enforces tenancy in Go, never
 * in Postgres), so this fixture's direct insert is exactly as authorized as
 * the API's own connection, it just skips the HTTP hop (of either PostgREST
 * or the API) and the maintainer check that exists to gate *real Vereine*
 * asking for a team, not a test seeding its own throwaway one.
 *
 * User creation and session minting still go through the Supabase Admin API
 * (GoTrue, `/auth/v1/admin/*`) — a different service from PostgREST, and one
 * this product's own login flow already depends on, so it stays as is.
 */

interface SessionCookie {
	readonly name: string;
	readonly value: string;
}

// The slug format caps at 40 characters and forbids a trailing hyphen, and these rows are
// deleted at the end of each test, so 48 bits of entropy is far more than the collision window
// needs — twelve hex characters, not the full UUID.
const TEAM_SLUG_RANDOM_HEX_LENGTH = 12;

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
 *
 * @param options - The pieces needed to mint the session.
 * @param options.admin - A Supabase Admin API client, reused here for `generateLink`.
 * @param options.url - The Supabase project URL, used to derive the session cookies' names.
 * @param options.serviceRoleKey - Any valid project key; GoTrue accepts it for `verifyOtp` regardless of role.
 * @param options.email - The address to mint a session for.
 * @returns The session cookies captured from the `@supabase/ssr` cookie sink.
 */
/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `admin` is
 * `@supabase/supabase-js`'s own `SupabaseClient`, whose generic type parameters carry nested
 * non-readonly members; wrapping the destructured parameter in `Readonly<>` doesn't reach them,
 * and that type isn't ours to edit.
 */
async function mintSessionCookies({
	admin,
	url,
	serviceRoleKey,
	email,
}: Readonly<{
	admin: SupabaseClient;
	url: string;
	serviceRoleKey: string;
	email: string;
}>): Promise<SessionCookie[]> {
	const captured: SessionCookie[] = [];

	// The service-role key here (where this call conventionally takes the
	// anon/publishable key) doesn't affect cookie naming or encoding — that's
	// derived only from `url`'s hostname — it only authenticates the request
	// GoTrue makes on `verifyOtp` below, and GoTrue accepts any valid project
	// key for that.
	const cookieClient = createServerClient(url, serviceRoleKey, {
		cookies: {
			getAll: () => [],
			/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `cookies`'
			 * inferred element type carries `@supabase/ssr`'s own `CookieOptions`, which nests a
			 * non-readonly property from the `cookie` package; that type isn't ours to edit (see
			 * `apps/web/src/server/supabase.ts`'s `serialize`, which hits the identical cause).
			 */
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
 *
 * @param cookies - The session cookies minted by `mintSessionCookies`.
 * @param baseURL - The app's base URL; its scheme and hostname decide whether `secure` is forced.
 * @returns Cookie descriptors ready for `context.addCookies`.
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

interface Team {
	readonly id: string;
	readonly name: string;
	readonly slug: string;
}

/**
 * Reads and validates the three environment variables authenticated e2e needs, throwing one
 * combined error naming every missing one — without them these specs would run signed out and
 * pass against the login page, the same failure the protection-bypass work fixed in September.
 *
 * @returns The validated `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` and `E2E_DATABASE_URL`.
 */
function requireE2eEnv(): { databaseUrl: string; serviceRoleKey: string; url: string } {
	const url = process.env.SUPABASE_URL;
	const serviceRoleKey = process.env.SUPABASE_SERVICE_ROLE_KEY;
	const databaseUrl = process.env.E2E_DATABASE_URL;
	if (
		url === undefined ||
		url === '' ||
		serviceRoleKey === undefined ||
		serviceRoleKey === '' ||
		databaseUrl === undefined ||
		databaseUrl === ''
	) {
		const missing: string[] = [];
		if (url === undefined || url === '') missing.push('SUPABASE_URL');
		if (serviceRoleKey === undefined || serviceRoleKey === '')
			missing.push('SUPABASE_SERVICE_ROLE_KEY');
		if (databaseUrl === undefined || databaseUrl === '') missing.push('E2E_DATABASE_URL');
		throw new Error(
			`${missing.join(', ')} ${missing.length === 1 ? 'is' : 'are'} required for authenticated e2e. ` +
				'Without them these specs would run signed out and pass against the login page — ' +
				'the same failure the protection-bypass work fixed in September.',
		);
	}
	return { databaseUrl, serviceRoleKey, url };
}

/**
 * Seeds the fixture's throwaway team and its owning membership row directly over Postgres,
 * bypassing `POST /v1/teams` — that endpoint is restricted to `MAINTAINER_USER_IDS`, a fixed
 * allowlist a freshly-minted test user can never be on (see this file's top-of-file comment).
 *
 * @param db - The e2e database connection, already open.
 * @param userId - The fixture user who owns the new team.
 * @returns The seeded team's id, name and slug.
 */
/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `db` is `node-postgres`'s
 * own `Client`, whose `query`/`connect`/`end` methods mutate its connection state; that type
 * isn't ours to edit, and wrapping it in `Readonly<>` was tried and still fails the check.
 */
async function seedFixtureTeam(db: PgClient, userId: string): Promise<Team> {
	// Captured in its own variable, not inlined into the query's parameter
	// array, so the exact string this run generated can be handed back to
	// the caller (below) alongside the id — `i18n.spec.ts` needs the literal
	// value to tell its own crawl "this string is this run's team name, not
	// hardcoded UI copy" (`teamName` fixture, further down).
	const teamName = `e2e ${randomUUID()}`;
	const teamSlug = `e2e-${randomUUID().replaceAll('-', '').slice(0, TEAM_SLUG_RANDOM_HEX_LENGTH)}`;
	const teamResult = await db.query<{ id: string }>(
		'insert into team (name, slug) values ($1, $2) returning id',
		[teamName, teamSlug],
	);
	const teamRow = teamResult.rows[0];
	if (teamRow === undefined) {
		throw new Error('could not seed the e2e fixture team: insert returned no row');
	}

	await db.query('insert into team_member (team_id, user_id, role) values ($1, $2, $3)', [
		teamRow.id,
		userId,
		'owner',
	]);

	return { id: teamRow.id, name: teamName, slug: teamSlug };
}

export const test = base.extend<{
	team: Team;
	teamId: string;
	teamName: string;
	teamSlug: string;
}>({
	/* oxlint-disable-next-line eslint/max-statements, typescript/prefer-readonly-parameter-types --
	 * two independent reasons on one line. `max-statements`: Playwright's fixture API gives setup,
	 * the one `use()` call and teardown exactly one shared closure (not separate hooks),
	 * specifically so a `finally` can guarantee cleanup however far setup got — `db`, `admin`,
	 * `userId` and `teamId` all have to be live across that whole span; `requireE2eEnv` and
	 * `seedFixtureTeam` above already carry the two pieces that don't need to share that span, and
	 * threading the rest through further functions would be indirection, not simplification, for
	 * what one fixture is inherently doing. `prefer-readonly-parameter-types`: Playwright's own
	 * fixture argument type nests `BrowserContext`, whose methods (`addCookies` among them) mutate;
	 * that type isn't ours to edit, and this fixture calls exactly that method below.
	 */
	team: async ({ context, baseURL }, use): Promise<void> => {
		const { databaseUrl, serviceRoleKey, url } = requireE2eEnv();
		// playwright.config.ts always sets `use.baseURL` (to BASE_URL or the
		// localhost fallback), so this is only ever undefined if that invariant
		// is broken — worth a loud failure rather than silently addressing
		// cookies to a wrong host.
		if (baseURL === undefined)
			throw new Error('baseURL fixture is unset — check playwright.config.ts');

		const admin = createClient(url, serviceRoleKey);
		const db = new PgClient({ connectionString: databaseUrl });
		await db.connect();

		const email = `e2e-${randomUUID()}@example.com`;

		let userId: string | undefined = undefined;
		let teamId: string | undefined = undefined;

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

			const team = await seedFixtureTeam(db, userId);
			teamId = team.id;

			const cookies = await mintSessionCookies({ admin, email, serviceRoleKey, url });
			await context.addCookies(toBrowserCookies(cookies, baseURL));

			await use(team);
		} finally {
			// Team first, then user: `link.created_by` references `auth.users`
			// with no `on delete cascade` (deliberately — see the migration's own
			// comment), so a user deleted first would fail on any link this test
			// created for the team. `team_member`/`link`/`domain`/`tag` all cascade
			// from `team` itself (`on delete cascade`), so deleting the team first
			// leaves nothing behind for the user delete to trip over.
			if (teamId !== undefined) await db.query('delete from team where id = $1', [teamId]);
			await db.end();
			if (userId !== undefined) await admin.auth.admin.deleteUser(userId);
		}
	},

	/**
	 * Thin derivatives over `team`, not their own independent setup: Playwright
	 * scopes a fixture's provisioning/teardown to run once per test regardless
	 * of how many other fixtures depend on it, so `links.spec.ts` destructuring
	 * only `teamSlug` (as it did before `team` existed) still gets exactly one
	 * team created and torn down, not two. `i18n.spec.ts`'s authenticated crawl
	 * is the one caller that needs `teamName` too. `teamId` stays for direct
	 * database assertions — it is a real column value, not a URL fragment —
	 * while `teamSlug` is what every spec now builds `/teams/...` URLs from,
	 * since that is what the app itself routes on.
	 *
	 * @param root0 - The fixtures this one depends on.
	 * @param root0.team - The provisioned team to read the id from.
	 * @param use - Playwright's callback, given this fixture's value.
	 */
	teamId: async ({ team }: { readonly team: Team }, use): Promise<void> => {
		await use(team.id);
	},

	teamName: async ({ team }: { readonly team: Team }, use): Promise<void> => {
		await use(team.name);
	},

	teamSlug: async ({ team }: { readonly team: Team }, use): Promise<void> => {
		await use(team.slug);
	},
});
