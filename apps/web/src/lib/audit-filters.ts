/** A calendar day exactly as the controls produce it and the URL carries it. */
const DAY = /^\d{4}-\d{2}-\d{2}$/u;

/** The entity types `GET /v1/teams/{team_id}/audit-log` accepts, as its own enum declares them. */
const AUDIT_ENTITY_TYPES = ['domain', 'folder', 'link', 'tag', 'team', 'team_member'] as const;

type AuditEntityType = (typeof AUDIT_ENTITY_TYPES)[number];

/**
 * Takes `unknown` rather than `string` because its harder caller has nothing
 * better to offer: `parseAuditFilters` below is handed whatever a bookmark or
 * a hand-edited URL put in the search parameters, and narrowing that to the
 * union in one place is what keeps every reader of `AuditFilters.entityType`
 * from having to re-check it. `audit-filter-bar.tsx` calls the same guard on
 * its own `<select>`'s value — a value it put there itself, so the check is
 * redundant there, but a second copy of a list-membership test is how the two
 * come to disagree the day a seventh entity type is added.
 *
 * @param value - The value to check.
 * @returns Whether it is one of the entity types the endpoint accepts.
 */
function isAuditEntityType(value: unknown): value is AuditEntityType {
	return typeof value === 'string' && (AUDIT_ENTITY_TYPES as readonly string[]).includes(value);
}

/** The page's filters, exactly as they live in the route's search parameters. */
interface AuditFilters {
	/** A user id, passed through opaquely; the API validates it and answers 422 itself. */
	readonly actor?: string;
	readonly entityType?: AuditEntityType;
	/** The first day to include, as YYYY-MM-DD. */
	readonly from?: string;
	/** 1-based, like every other paginated list in this app. */
	readonly page: number;
	/** The last day to include, as YYYY-MM-DD. */
	readonly to?: string;
}

/**
 * Anything at all can arrive in a search parameter — a bookmark from an older
 * build, a hand-edited URL, a stale link in an email. Every value is therefore
 * checked rather than cast, and a value that fails is dropped rather than
 * corrected: a filter the reader did not ask for is worse than no filter,
 * because the page would then quietly show a subset while looking complete.
 *
 * @param search - The route's raw search object.
 * @returns The filters, with every unusable value removed.
 */
function parseAuditFilters(
	search: Readonly<{
		actor?: unknown;
		entityType?: unknown;
		from?: unknown;
		page?: unknown;
		to?: unknown;
	}>,
): AuditFilters {
	const page =
		typeof search.page === 'number' && Number.isInteger(search.page) && search.page >= 1
			? search.page
			: 1;

	return {
		...(typeof search.actor === 'string' && search.actor !== '' && { actor: search.actor }),
		...(isAuditEntityType(search.entityType) && {
			entityType: search.entityType,
		}),
		...(typeof search.from === 'string' && DAY.test(search.from) && { from: search.from }),
		page,
		...(typeof search.to === 'string' && DAY.test(search.to) && { to: search.to }),
	};
}

/**
 * Whether the reader narrowed the log at all.
 *
 * `page` is deliberately not one of these: it is where the reader is, not
 * what they asked to see, and counting it would make every page after the
 * first claim to be filtered.
 *
 * It lives here, next to the type it reads, because two places ask it for
 * two different reasons and neither may drift from the other: the filter bar
 * shows its "clear the filters" button on it, and the page chooses between
 * its two empty states on it. A reader who cleared every filter and still saw
 * "no changes match these filters" — or the reverse — would be looking at
 * exactly that drift.
 *
 * @param filters - The active filters.
 * @returns Whether any filter other than the page is set.
 */
function hasActiveFilters(filters: Readonly<AuditFilters>): boolean {
	return (
		filters.actor !== undefined ||
		filters.entityType !== undefined ||
		filters.from !== undefined ||
		filters.to !== undefined
	);
}

/**
 * Turns the two chosen days into the instants the endpoint filters on.
 *
 * `to` becomes the **last** millisecond of its day, not its first. The endpoint
 * compares `created_at <= to` against a `timestamptz`, so a day sent as
 * midnight would exclude everything that happened during it — choose "to:
 * today" and today's entries vanish, which reads as a broken log rather than
 * an off-by-one.
 *
 * Both are read as UTC, the convention the statistics window already uses
 * because the click rollup buckets in UTC. A reader west of Greenwich choosing
 * "today" therefore gets the UTC day, which is the same answer every other
 * date in this product gives.
 *
 * @param filters - The parsed filters.
 * @returns The `from`/`to` query values, each omitted when its day was not chosen.
 */
function toQueryRange(filters: Readonly<AuditFilters>): {
	from?: string;
	to?: string;
} {
	return {
		...(filters.from !== undefined && { from: `${filters.from}T00:00:00.000Z` }),
		...(filters.to !== undefined && { to: `${filters.to}T23:59:59.999Z` }),
	};
}

export { AUDIT_ENTITY_TYPES, hasActiveFilters, isAuditEntityType, parseAuditFilters, toQueryRange };
export type { AuditEntityType, AuditFilters };
