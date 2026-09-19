import type { AuditEntry, Member, PageAuditEntry, PageMember } from '@kurze-url/api-client';
import {
	createFileRoute,
	Link as RouterLink,
	Navigate,
	notFound,
	type SearchSchemaInput,
} from '@tanstack/react-router';
import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';

import { AuditEntryTable } from '../../components/audit-entry-table';
import { AuditFilterBar } from '../../components/audit-filter-bar';
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from '../../components/ui/empty';
import { Pagination, PaginationContent, PaginationItem } from '../../components/ui/pagination';
import { classifyApiError, statusOf, type ApiFailure } from '../../lib/api-errors';
import { parseAuditFilters, type AuditFilters } from '../../lib/audit-filters';
import { reportUnexpected } from '../../lib/observability';
import type { Language } from '../../lib/preferences';
import { usePreferences } from '../../lib/use-preferences';
import { AUDIT_LOG_PER_PAGE, auditLogQueryOptions } from '../../server/audit-log';
import { membersQueryOptions } from '../../server/members';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below traces to
   TanStack Router's own `validateSearch`/`beforeLoad`/`loaderDeps`/`loader` option shapes, or to
   `@kurze-url/api-client`'s generated `AuditEntry`/`Member` types, whose properties are mutable —
   generated codegen output, never edited by hand. Neither is a declaration this file owns. */

/** RFC 9110's "Forbidden", named rather than written as a bare literal at the one place it is read. */
const HTTP_FORBIDDEN = 403;

/**
 * What the loader hands the page. A union rather than one flat object with
 * empty arrays in it: a refused read has no entries, no members and no
 * total, and inventing zeroes for them would make the refusal
 * indistinguishable, at the type level, from a team whose log is genuinely
 * empty — which is the one pair of states this page exists to tell apart.
 */
type AuditLogPageData =
	| {
			readonly entries: readonly AuditEntry[];
			readonly forbidden: false;
			readonly members: readonly Member[];
			readonly page: number;
			readonly total: number;
	  }
	| { readonly forbidden: true };

/**
 * Both reads in parallel, the way `loadStatsPage` pairs its own two: the log
 * carries `actor_user_id` as a bare UUID and nothing else, so without the
 * member list no row could name who made the change.
 *
 * The two fetches arrive as thunks rather than as one narrow `{ query }`
 * client interface — `StatsDataSource`'s shape in
 * `teams.$teamSlug.links.$linkId_.stats.tsx` — because that shape does not
 * survive a second query type here. One interface covering both would need
 * an overloaded `query`, and checking `QueryClient` against an overloaded
 * target instantiates its generic signature with `any` rather than
 * inferring: `QueryExecuteOptions`'s `TPageParam` defaults to `never`, and
 * `QueryFunction<…, never>` is not assignable to `QueryFunction<any, any,
 * any>`, so `tsc` refuses with TS2322 ("Type 'any' is not assignable to
 * type 'never'"). A single-signature interface — which is all the statistics
 * page needs — infers instead and is accepted, which is why that file can
 * use one and this one cannot. Thunks keep the same property that interface
 * was there for: this function is callable, and testable, with no
 * `QueryClient` anywhere near it.
 *
 * The two refusals this endpoint can answer with are **not** the same page.
 * `GET /v1/teams/{id}/audit-log` embeds `authz.AdminScope`, which answers a
 * non-member with 404 (a team's existence is never disclosed) and a member
 * below admin with 403. `classifyApiError` deliberately collapses both into
 * `notFound` — `api-errors.test.ts` pins that as "maps 403 to notFound, not
 * to a forbidden state", because everywhere else in this app rendering
 * "forbidden" would leak exactly what the API withholds. Here the two differ:
 * the caller is a known member of a team they can already see, so telling
 * them the history is admin-only discloses nothing they did not already
 * know. `statusOf` — exported for precisely "the rare call site that needs
 * the raw HTTP status alongside `ApiFailure`'s kind", and already used that
 * way for a 409 in `teams.$teamSlug.domains.tsx` — is what splits them, and
 * it has to be read **before** `classifyApiError`, since that call would
 * swallow the 403 into `notFound` and send an admin-less member to the
 * "page not found" page instead of an explanation.
 *
 * A 404 is normalised to the router's own `notFound()`, the same way
 * `loadStats` does it, so it lands on the root's not-found page rather than
 * this route's error boundary.
 *
 * Both envelopes' `items` are `Array<T> | null` — Huma serialises a nil Go
 * slice as JSON `null`, the same shape `link-list.tsx` normalises — so both
 * are coalesced here, once, rather than at every reader. `AuditEntryTable`
 * declares a non-null array, so without this a team with no history at all
 * would crash on the very state `audit.emptyUnfiltered` exists for.
 *
 * @param options - The dependencies this loader composes.
 * @param options.fetchLog - Fetches the requested page of the team's audit log.
 * @param options.fetchMembers - Fetches the team's members, for naming each entry's actor.
 * @returns The page of entries and the member list, or the admin refusal.
 */
export async function loadAuditLogPage(
	options: Readonly<{
		fetchLog: () => Promise<PageAuditEntry>;
		fetchMembers: () => Promise<PageMember>;
	}>,
): Promise<AuditLogPageData> {
	try {
		const [log, members] = await Promise.all([options.fetchLog(), options.fetchMembers()]);

		return {
			entries: log.items ?? [],
			forbidden: false,
			members: members.items ?? [],
			page: log.page,
			total: log.total_count,
		};
	} catch (error) {
		if (statusOf(error) === HTTP_FORBIDDEN) return { forbidden: true };
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `notFound()` is its control flow, not an Error.
		if (classifyApiError(error).kind === 'notFound') throw notFound();
		throw error;
	}
}

/**
 * Whether the reader narrowed the log at all. `page` is deliberately not one
 * of these: it is where the reader is, not what they asked to see, and
 * counting it would make every second page claim to be filtered.
 *
 * `AuditFilterBar` computes the same four-way check for its own "clear the
 * filters" button and does not export it — the same trade its own
 * `isAuditEntityType` note records: repeating a one-line check is cheaper
 * than exporting one for a second caller that asks it for a different
 * reason.
 *
 * @param filters - The active filters.
 * @returns Whether any filter other than the page is set.
 */
function hasAnyFilter(filters: Readonly<AuditFilters>): boolean {
	return (
		filters.actor !== undefined ||
		filters.entityType !== undefined ||
		filters.from !== undefined ||
		filters.to !== undefined
	);
}

// oxlint-disable-next-line sort-keys -- `validateSearch` has to stay declared before `loaderDeps`/`loader`: see the comment on it below.
export const Route = createFileRoute('/_authed/teams/$teamSlug/audit-log')({
	// Declared before loaderDeps/loader, not for readability: loaderDeps's own
	// `search` parameter is typed from this function's return type, and moving
	// it later makes that inference fall back to {}. The statistics route and
	// the links index both carry the same note for the same reason.
	//
	// The parameter type intersects `SearchSchemaInput` so a `<Link>` to this
	// route — the sidebar entry, for one — can omit every filter and still
	// land on page 1 without knowing this page's search schema.
	validateSearch: (
		search: {
			actor?: string;
			entityType?: string;
			from?: string;
			page?: number;
			to?: string;
		} & SearchSchemaInput,
	): AuditFilters => parseAuditFilters(search),
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	loaderDeps: ({ search }) => search,
	// `query()`, not the `@deprecated` `ensureQueryData`: the installed
	// `@tanstack/query-core` names `query({ ...options, staleTime: 'static' })`
	// as its replacement. `staleTime` has to travel with it — `query()`'s own
	// default is `0`, so each fetch would be stale the instant it landed and
	// eligible for a silent refetch the moment anything else read the key.
	loader: async ({ context, deps }) =>
		loadAuditLogPage({
			fetchLog: async () =>
				context.queryClient.query({
					...auditLogQueryOptions(context.teamId, deps),
					staleTime: 'static',
				}),
			fetchMembers: async () =>
				context.queryClient.query({
					...membersQueryOptions(context.teamId),
					staleTime: 'static',
				}),
		}),
	component: RouteComponent,
	errorComponent: AuditLogError,
});

/**
 * Same shape as `StatsError`/`LinksError`/`DomainsError`: a route-level
 * `errorComponent` is the nearest one TanStack Router renders, so without
 * reporting here a 500 from this endpoint would never reach `RootErrorPage`
 * and no event would ever be sent. Neither refusal arrives here — the loader
 * turns a 403 into the page's own refusal state and a 404 into `notFound()`
 * — so what is left is a genuinely unexpected failure, which is exactly what
 * `reportUnexpected` is for.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function AuditLogError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	reportUnexpected(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

/**
 * What a member below admin sees. It keeps the page's `<h1>` — the section
 * is real and the reader navigated to it deliberately — and the explanation
 * hangs off it as an `<h2>`, so the refusal has a place in the heading
 * outline rather than being a paragraph floating under a title.
 *
 * Neither the intro nor the filter bar renders: both describe entries this
 * reader cannot see, and offering controls that can only produce the same
 * refusal is worse than offering none.
 *
 * Exported so `teams.$teamSlug.audit-log.a11y.test.tsx` can run axe over the
 * state production actually renders, rather than a hand-kept copy of it —
 * the same reason `AuditLogPageBody` below is exported.
 *
 * @returns The rendered refusal.
 */
export function AuditLogForbidden(): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<>
			<h1>{t('audit.heading')}</h1>
			<Empty>
				<EmptyHeader>
					{/* `EmptyTitle` hardcodes a `<div>` with no `render` prop, so a real
					    `<h2>` nests inside it rather than reaching for `role="heading"`,
					    which `jsx-a11y/prefer-tag-over-role` refuses on a `<div>` — the
					    same pattern the statistics page's own empty states use. Without
					    it, this state's whole heading outline is the `<h1>` above. */}
					<EmptyTitle>
						<h2>{t('audit.forbiddenTitle')}</h2>
					</EmptyTitle>
					<EmptyDescription>{t('audit.forbiddenBody')}</EmptyDescription>
				</EmptyHeader>
			</Empty>
		</>
	);
}

export interface AuditLogPageBodyProps {
	/** The page of entries to render, newest first — the API query's own order, never re-sorted. */
	readonly entries: readonly AuditEntry[];
	/** The filters as they currently live in the route's search parameters. */
	readonly filters: AuditFilters;
	/** The active language, for date formatting. */
	readonly language: Language;
	/** The team's current members, for the actor filter and for naming each entry's actor. */
	readonly members: readonly Member[];
	/** Called with the whole next `AuditFilters`, page already reset to `1` by the filter bar. */
	readonly onFiltersChange: (filters: AuditFilters) => void;
	/**
	 * The page the API answered with, which is what the pagination arithmetic
	 * is based on — `filters.page` is what was *asked* for, and the two are
	 * only the same for as long as the endpoint keeps echoing the request back.
	 */
	readonly page: number;
	/** The team slug, for the pagination links' route params. */
	readonly teamSlug: string;
	/** How many entries match the active filters across every page. */
	readonly total: number;
}

/**
 * The presentational body of a team's audit log — pure and prop-driven, the
 * same idiom `StatsPageBody`/`LinkList`/`AuthedShell` already use so a
 * route's router wiring can be tested separately from what it renders.
 * `RouteComponent` below owns that wiring (`Route.useParams`,
 * `Route.useLoaderData`, `Route.useSearch`, `usePreferences`,
 * `Route.useNavigate`) and passes plain data and a callback in here.
 *
 * The two empty states are not interchangeable: `audit.empty` says the
 * filters matched nothing, which in front of a reader who set no filters is
 * a small lie about a page that is simply new. `audit.emptyUnfiltered` is
 * what a team with no history yet gets.
 *
 * The filter bar renders in both states. A reader who filtered their way to
 * nothing needs the control that got them there in order to get back out.
 *
 * @param props - The component's props.
 * @param props.entries - The page of entries to render, newest first.
 * @param props.filters - The filters as they currently live in the route's search parameters.
 * @param props.language - The active language, for date formatting.
 * @param props.members - The team's current members.
 * @param props.onFiltersChange - Called with the whole next `AuditFilters`.
 * @param props.page - The page the API answered with.
 * @param props.teamSlug - The team slug, for the pagination links' route params.
 * @param props.total - How many entries match the active filters across every page.
 * @returns The rendered page body.
 */
export function AuditLogPageBody({
	entries,
	filters,
	language,
	members,
	onFiltersChange,
	page,
	teamSlug,
	total,
}: AuditLogPageBodyProps): React.JSX.Element {
	const { t } = useTranslation();
	// Memoised because `AuditEntryTable` re-renders on every disclosure
	// toggle, and rebuilding one entry per member each time would be work for
	// nothing. `AuditFilterBar` keeps the array instead — it lists members, it
	// does not look them up.
	const membersById = useMemo(
		() => new Map(members.map((member) => [member.user_id, member.email])),
		[members],
	);

	const hasPreviousPage = page > 1;
	const hasNextPage = page * AUDIT_LOG_PER_PAGE < total;

	return (
		<>
			<h1>{t('audit.heading')}</h1>
			<p>{t('audit.intro')}</p>

			<AuditFilterBar filters={filters} members={members} onChange={onFiltersChange} />

			{entries.length === 0 ? (
				<Empty>
					<EmptyDescription>
						{t(hasAnyFilter(filters) ? 'audit.empty' : 'audit.emptyUnfiltered')}
					</EmptyDescription>
				</Empty>
			) : (
				<>
					<AuditEntryTable entries={entries} language={language} membersById={membersById} />
					{/* The pagination chrome is the same block `link-list.tsx` renders,
					    down to its catalogue keys: "Pagination", "Previous page" and
					    "Next page" name the control, not the thing being paged, and
					    Task 4 shipped no `audit.*` equivalents precisely because there
					    is nothing audit-specific to say here. */}
					<Pagination aria-label={t('links.paginationLabel')}>
						<PaginationContent>
							<PaginationItem>
								{hasPreviousPage ? (
									<RouterLink
										params={{ teamSlug }}
										search={{ ...filters, page: page - 1 }}
										to="/teams/$teamSlug/audit-log"
									>
										{t('links.previousPage')}
									</RouterLink>
								) : (
									<span aria-disabled="true">{t('links.previousPage')}</span>
								)}
							</PaginationItem>
							<PaginationItem>
								{hasNextPage ? (
									<RouterLink
										params={{ teamSlug }}
										search={{ ...filters, page: page + 1 }}
										to="/teams/$teamSlug/audit-log"
									>
										{t('links.nextPage')}
									</RouterLink>
								) : (
									<span aria-disabled="true">{t('links.nextPage')}</span>
								)}
							</PaginationItem>
						</PaginationContent>
					</Pagination>
				</>
			)}
		</>
	);
}

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const data = Route.useLoaderData();
	const filters = Route.useSearch();
	const { language } = usePreferences();
	const navigate = Route.useNavigate();

	if (data.forbidden) return <AuditLogForbidden />;

	return (
		<AuditLogPageBody
			entries={data.entries}
			filters={filters}
			language={language}
			members={data.members}
			onFiltersChange={(next) => {
				void navigate({ search: next });
			}}
			page={data.page}
			teamSlug={teamSlug}
			total={data.total}
		/>
	);
}
