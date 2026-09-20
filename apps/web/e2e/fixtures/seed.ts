import { randomUUID } from 'node:crypto';

import { createClient, type SupabaseClient } from '@supabase/supabase-js';
import { Client as PgClient } from 'pg';

import { requireE2eEnv } from './env';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- `db` is `node-postgres`'s own
 * `Client`, whose `query`/`connect`/`end` methods mutate its connection state; that type isn't
 * ours to edit, and `./auth` carries the identical note for the identical reason.
 */

/**
 * The rollup rows a spec needs cannot be produced by driving the app: click
 * recording hangs off the redirect path, which writes asynchronously, buckets
 * by day, and finishes at no time a test can wait for. So these go straight
 * into `link_click_stats`, over the same kind of direct Postgres connection the
 * team fixture already uses and for a related reason — see `./auth`'s
 * top-of-file comment on why a direct connection is not a lesser form of
 * authorization here.
 *
 * What that trades away is stated plainly: a spec seeded this way proves the
 * page renders recorded clicks, never that a redirect records them. That half
 * belongs to `apps/api`, where `internal/analytics` and `internal/db` test it
 * against a real database.
 *
 * Nothing here needs its own teardown. `link_click_stats.link_id` references
 * `link` `on delete cascade` and `link` cascades from `team`, so the team
 * deletion the `team` fixture already performs takes these rows with it.
 */

/** One day of seeded rollup, and the split every dimension applies to it. */
interface DaySeed {
	/** The day's `total` row. */
	readonly clicks: number;
	/** How far back to place the bucket, counted from the database's own `current_date`. */
	readonly daysAgo: number;
	/** The `bot_status = 'human'` share; the `bot` row takes the remainder. */
	readonly humanClicks: number;
	readonly humanUniqueVisitors: number;
	/** The leading value's share of every other dimension; its second value takes the remainder. */
	readonly primaryClicks: number;
	readonly primaryUniqueVisitors: number;
	readonly uniqueVisitors: number;
}

/** One row on its way into `link_click_stats`. */
interface SeedRow {
	readonly clicks: number;
	readonly daysAgo: number;
	readonly type: string;
	readonly uniqueVisitors: number;
	/** Null exactly for the `total` row, as the table's own check constraint requires. */
	readonly value: string | null;
}

/** The four values `team_member.role` may hold, as the initial migration's own check constraint lists them. */
type TeamRole = 'admin' | 'editor' | 'owner' | 'viewer';

/**
 * Three days, deliberately none of them the database's own today.
 *
 * The API derives its window from Go's clock and this insert derives its
 * buckets from Postgres's, and a run crossing midnight between the two would
 * put a row one day past the window's `to` bound, where the page would simply
 * not show it. Starting at yesterday costs nothing and removes that race.
 *
 * The figures are chosen so every number the assertions look for is unique
 * across the whole rendered page: totals 25/19, the human split 18/13, a
 * breakdown's leading value 15/11 and its second 10/8. A repeated figure would
 * let an assertion pass against the wrong element.
 *
 * `seedLinkClicks` can push all three further back through its `daysAgoOffset`
 * option, which is how a spec places data outside the page's default 30-day
 * window without inventing a second seed.
 */
const DAYS: readonly DaySeed[] = [
	{
		clicks: 5,
		daysAgo: 3,
		humanClicks: 4,
		humanUniqueVisitors: 3,
		primaryClicks: 3,
		primaryUniqueVisitors: 2,
		uniqueVisitors: 4,
	},
	{
		clicks: 8,
		daysAgo: 2,
		humanClicks: 6,
		humanUniqueVisitors: 4,
		primaryClicks: 5,
		primaryUniqueVisitors: 4,
		uniqueVisitors: 6,
	},
	{
		clicks: 12,
		daysAgo: 1,
		humanClicks: 8,
		humanUniqueVisitors: 6,
		primaryClicks: 7,
		primaryUniqueVisitors: 5,
		uniqueVisitors: 9,
	},
];

/**
 * The dimensions every click writes besides `total` and `bot_status`, each with
 * the value that leads it and the value that takes the remainder.
 *
 * The values are the ones `apps/api/internal/analytics/dimensions.go` actually
 * produces — a browser name from the user-agent parser, an uppercase country
 * code from Vercel's geo header, `direct` for a missing referrer, one of
 * `desktop`/`mobile`/`tablet`/`unknown` for the device. A seed of plausible but
 * impossible values would be a test asserting fiction.
 */
const DIMENSIONS: readonly (readonly [type: string, primary: string, secondary: string])[] = [
	['qr_vs_regular', 'regular', 'qr'],
	['browser', 'Chrome', 'Firefox'],
	['os', 'macOS', 'Windows'],
	['device', 'desktop', 'mobile'],
	['country', 'DE', 'AT'],
	['referrer', 'direct', 'newsletter.example.org'],
	['utm_source', 'newsletter', 'flyer'],
];

/**
 * The dimension types whose values reach the screen as themselves.
 * `StatSummary` maps `bot_status` and `qr_vs_regular` through translation keys
 * (`stats.dimensionValueHuman` and its three siblings) instead, so those four
 * values are copy on screen, not data.
 */
const RENDERED_RAW = new Set(['browser', 'country', 'device', 'os', 'referrer', 'utm_source']);

/**
 * `unnest` over five parallel arrays rather than a generated `values` list: one
 * statement with five parameters whatever the seed's size, instead of a
 * placeholder string this file would have to build and keep aligned with the
 * column order by hand.
 *
 * `current_date` comes from the database, not from this process — see `DAYS` on
 * why the two clocks are kept from having to agree.
 */
const INSERT_ROWS = `insert into link_click_stats
     (link_id, bucket_start, dimension_type, dimension_value, clicks, unique_visitors)
   select $1, current_date - seed.days_ago, seed.dimension_type, seed.dimension_value,
          seed.clicks, seed.unique_visitors
   from unnest($2::int[], $3::text[], $4::text[], $5::bigint[], $6::bigint[])
     as seed(days_ago, dimension_type, dimension_value, clicks, unique_visitors)`;

/**
 * Opens a connection, runs one thing with it, closes it again.
 *
 * A short-lived connection per call rather than a fixture holding one open:
 * only a handful of tests seed anything, and the `team` fixture's own client
 * lives inside that fixture's closure, where handing it out would mean exposing
 * a lifetime a spec could outlive.
 *
 * @param run - What to do with the open connection.
 * @returns Whatever `run` returned.
 */
async function withDb<T>(run: (db: PgClient) => Promise<T>): Promise<T> {
	const { databaseUrl } = requireE2eEnv();
	const db = new PgClient({ connectionString: databaseUrl });
	await db.connect();
	try {
		return await run(db);
	} finally {
		await db.end();
	}
}

/**
 * Expands one day into every row it writes: its `total`, both halves of the
 * human/bot split, and both values of each remaining dimension. Each day's
 * dimensions add up to that day's `total`, so the page's figures are consistent
 * with each other rather than merely non-empty.
 *
 * @param day - The day to expand.
 * @param offset - Extra days to push the bucket further back, so a spec can place data outside a default window.
 * @returns That day's rows.
 */
function rowsForDay(day: DaySeed, offset: number): SeedRow[] {
	return [
		{
			clicks: day.clicks,
			daysAgo: day.daysAgo + offset,
			type: 'total',
			uniqueVisitors: day.uniqueVisitors,
			value: null,
		},
		{
			clicks: day.humanClicks,
			daysAgo: day.daysAgo + offset,
			type: 'bot_status',
			uniqueVisitors: day.humanUniqueVisitors,
			value: 'human',
		},
		{
			clicks: day.clicks - day.humanClicks,
			daysAgo: day.daysAgo + offset,
			type: 'bot_status',
			uniqueVisitors: day.uniqueVisitors - day.humanUniqueVisitors,
			value: 'bot',
		},
		...DIMENSIONS.flatMap(([type, primary, secondary]) => [
			{
				clicks: day.primaryClicks,
				daysAgo: day.daysAgo + offset,
				type,
				uniqueVisitors: day.primaryUniqueVisitors,
				value: primary,
			},
			{
				clicks: day.clicks - day.primaryClicks,
				daysAgo: day.daysAgo + offset,
				type,
				uniqueVisitors: day.uniqueVisitors - day.primaryUniqueVisitors,
				value: secondary,
			},
		]),
	];
}

/**
 * Adds one field of every seeded day together.
 *
 * @param pick - Which field to read off each day.
 * @returns The sum across the whole seeded window.
 */
function total(pick: (day: DaySeed) => number): number {
	return DAYS.reduce((sum, day) => sum + pick(day), 0);
}

/**
 * Inserts the second member's `team_member` row, split out of
 * `seedSecondMember` so that function's own statement count stays within
 * this file's lint budget.
 *
 * @param options - The pieces needed to insert the row.
 * @param options.admin - The Supabase Admin API client, reused here to delete the user if the insert fails.
 * @param options.teamId - The team the membership row belongs to.
 * @param options.userId - The already-created user the membership row is for.
 * @param options.role - The role to grant.
 */
async function insertSecondMemberRow({
	admin,
	teamId,
	userId,
	role,
}: Readonly<{
	admin: SupabaseClient;
	teamId: string;
	userId: string;
	role: TeamRole;
}>): Promise<void> {
	try {
		await withDb(async (db) => {
			await db.query('insert into team_member (team_id, user_id, role) values ($1, $2, $3)', [
				teamId,
				userId,
				role,
			]);
		});
	} catch (error) {
		// The membership insert is what makes this user useful; a user left
		// behind by a failure here would leak exactly the way cleanup exists to
		// prevent.
		await admin.auth.admin.deleteUser(userId);
		throw error;
	}
}

/** What a spec needs back: the figures to assert, and the raw values the i18n crawl must exclude. */
export interface SeededClicks {
	/**
	 * The dimension values that reach the screen as themselves, in no particular
	 * order. `stat-breakdown-card.tsx` prints `value.value` raw, so these are
	 * identical in both languages and must be excluded from `i18n.spec.ts`'s
	 * comparison, the same way that crawl already excludes its team's name and
	 * its link's URLs.
	 */
	readonly dimensionValues: readonly string[];
	/** The figures every breakdown's leading row carries, identical across all six of them. */
	readonly leading: {
		readonly clicks: number;
		readonly uniqueVisitors: number;
	};
	/** The document's totals over the seeded window. */
	readonly totals: {
		readonly clicks: number;
		readonly humanClicks: number;
		readonly humanUniqueVisitors: number;
		readonly uniqueVisitors: number;
	};
}

/**
 * Reads the id of the team's one link.
 *
 * The page's own URL is `/teams/$teamSlug/links/$linkId/stats`, so a spec needs
 * the id — and the app never puts it anywhere readable except inside the list's
 * per-row edit `href`. Reading it from the database instead keeps that parsing
 * out of the specs: every `team` fixture provisions a fresh team, so "the
 * team's link" is unambiguous, and anything else is a bug worth failing on.
 *
 * @param teamId - The fixture team's id.
 * @returns The link's id.
 */
export async function linkIdForTeam(teamId: string): Promise<string> {
	return withDb(async (db) => {
		const result = await db.query<{ id: string }>('select id from link where team_id = $1', [
			teamId,
		]);
		const [row, ...rest] = result.rows;
		if (row === undefined || rest.length > 0) {
			throw new Error(
				`expected the fixture team to own exactly one link, found ${result.rows.length}`,
			);
		}
		return row.id;
	});
}

/**
 * Writes three days of rollup for one link.
 *
 * @param linkId - The link to record clicks against.
 * @param options - Where to place the seeded days.
 * @param options.daysAgoOffset - Extra days to push every bucket further back, so a spec can place the seeded window outside the page's default one. Defaults to 0.
 * @returns The totals and values a spec asserts on.
 */
export async function seedLinkClicks(
	linkId: string,
	options: Readonly<{ daysAgoOffset?: number }> = {},
): Promise<SeededClicks> {
	const offset = options.daysAgoOffset ?? 0;
	const rows = DAYS.flatMap((day) => rowsForDay(day, offset));

	await withDb(async (db) => {
		await db.query(INSERT_ROWS, [
			linkId,
			rows.map((row) => row.daysAgo),
			rows.map((row) => row.type),
			rows.map((row) => row.value),
			rows.map((row) => row.clicks),
			rows.map((row) => row.uniqueVisitors),
		]);
	});

	return {
		dimensionValues: DIMENSIONS.filter(([type]) => RENDERED_RAW.has(type)).flatMap(
			([, primary, secondary]) => [primary, secondary],
		),
		leading: {
			clicks: total((day) => day.primaryClicks),
			uniqueVisitors: total((day) => day.primaryUniqueVisitors),
		},
		totals: {
			clicks: total((day) => day.clicks),
			humanClicks: total((day) => day.humanClicks),
			humanUniqueVisitors: total((day) => day.humanUniqueVisitors),
			uniqueVisitors: total((day) => day.uniqueVisitors),
		},
	};
}

/**
 * Rewrites the role of the fixture team's one membership.
 *
 * `./auth`'s `team` fixture seeds its user as `owner` and every other spec's
 * session rests on that, so a spec needing a lesser role changes the row for
 * itself rather than parameterising the shared fixture — the alternative
 * would make one spec's requirement everyone else's default.
 *
 * The team id alone identifies the row: each test provisions a fresh team
 * with exactly one member. That assumption is checked rather than assumed,
 * because the failure it guards against is silent — an update matching no
 * row would leave the session an owner, and a spec asserting an entry is
 * *absent* would then pass for the wrong reason.
 *
 * Call it before the first `goto` that must see the new role. `GET /v1/me`
 * is read once per server render, and both gates this exists to exercise —
 * the sidebar entry and the endpoint's own `authz.AdminScope` — are decided
 * from that read and from the row itself.
 *
 * @param teamId - The fixture team whose membership to rewrite.
 * @param role - The role its user should have from now on.
 */
export async function setFixtureTeamRole(teamId: string, role: TeamRole): Promise<void> {
	await withDb(async (db) => {
		const result = await db.query('update team_member set role = $2 where team_id = $1', [
			teamId,
			role,
		]);
		if (result.rowCount !== 1) {
			throw new Error(
				`expected the fixture team to have exactly one membership, updated ${result.rowCount}`,
			);
		}
	});
}

/**
 * Adds a second person to the fixture team: a confirmed auth user plus its
 * membership row.
 *
 * `team_member.user_id` references `auth.users`, so a bare SQL insert cannot
 * do this on its own — the user has to exist first, which is why this reaches
 * for the Admin API the way `./auth`'s own fixture does.
 *
 * **Run the returned `cleanup` in a `finally`.** The `team` fixture's teardown
 * deletes the team and then *its own* user; it knows nothing about a second
 * one. The membership row does disappear on its own — `team_member.user_id`
 * is `on delete cascade` from `auth.users`
 * (`supabase/migrations/20260902075125_initial_schema.sql:17`) — but nothing
 * deletes the user, and a leaked row accumulates in the Preview Supabase
 * project on every run.
 *
 * A plain helper rather than a Playwright fixture on purpose: a fixture runs
 * before the test body, which would leave the team with two memberships
 * before `setFixtureTeamRole` could ever see exactly one, and that helper
 * refuses anything else.
 *
 * @param teamId - The fixture team to add the person to.
 * @param role - The role the second member should hold.
 * @returns The new member's address and id, and the cleanup its caller owes.
 */
export async function seedSecondMember(
	teamId: string,
	role: TeamRole,
): Promise<{ cleanup: () => Promise<void>; email: string; userId: string }> {
	const { serviceRoleKey, url } = requireE2eEnv();
	const admin = createClient(url, serviceRoleKey);
	const email = `e2e-second-${randomUUID()}@example.com`;

	const { data: created, error: createUserError } = await admin.auth.admin.createUser({
		email,
		email_confirm: true,
	});
	if (createUserError) {
		throw new Error(`could not create the second e2e member: ${createUserError.message}`);
	}
	const userId = created.user.id;

	await insertSecondMemberRow({ admin, role, teamId, userId });

	return {
		cleanup: async () => {
			await admin.auth.admin.deleteUser(userId);
		},
		email,
		userId,
	};
}
