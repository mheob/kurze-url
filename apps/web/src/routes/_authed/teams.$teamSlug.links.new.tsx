import type { CreateLinkInputBodyWritable, PageDomain } from '@kurze-url/api-client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, Link, useRouter, type SearchSchemaInput } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { LinkForm, type LinkFormValues } from '../../components/link-form';
import type { TagOption } from '../../components/tag-picker';
import { useLinkFormTags } from '../../hooks/use-link-form-tags';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { parseFolderIdSearch, remapFolderGoneFailure } from '../../lib/folders';
import { parseUuidSearch } from '../../lib/names';
import { remapTagGoneFailure } from '../../lib/tags';
import { canEdit } from '../../lib/team-roles';
import { domainsQueryOptions } from '../../server/domains';
import { foldersQueryOptions, prefetchFolders } from '../../server/folders';
import { createLinkFn } from '../../server/links';
import { prefetchTags } from '../../server/tags';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is typed by
   something this file doesn't own: TanStack Query's own `domainsQueryOptions` return type,
   TanStack Router's `beforeLoad`/`loader` option shapes, or the generated `@kurze-url/api-client`
   `Domain` type (`page.items`'s element type, inferred in the `filter`/`map` callbacks below). */

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `LinksDataSource` in the list route.
 */
interface DomainsDataSource {
	readonly ensureQueryData: (
		options: ReturnType<typeof domainsQueryOptions>,
	) => Promise<PageDomain>;
}

/**
 * Verified domains only: a `pending`/`failed` one has no working DNS yet, so
 * offering it in the picker would let a link get created on a hostname that
 * doesn't redirect. Falls back to an empty list on any failure — including
 * an expired session — rather than blocking the whole create-link page: the
 * picker is an enhancement over the shared hostname the form already falls
 * back to, and the create mutation's own `onError` already sends the visitor
 * to `/login` the moment they try to submit against a session that is
 * actually gone. Mirrors `listDomainsFor`'s own normalisation of a nil items
 * slice (Huma serialises it as JSON `null`).
 *
 * @param queryClient - The query client to fetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns The team's verified domains, or an empty list on any failure.
 */
export async function loadVerifiedDomains(
	queryClient: DomainsDataSource,
	teamId: string,
): Promise<readonly { id: string; hostname: string }[]> {
	try {
		const page = await queryClient.ensureQueryData(domainsQueryOptions(teamId));
		return (page.items ?? [])
			.filter((domain) => domain.verification_status === 'verified')
			.map((domain) => ({ hostname: domain.hostname, id: domain.id }));
	} catch (error) {
		// The fallback below must stay silent to the visitor — see the doc
		// comment above — but "silent" must not mean "invisible everywhere".
		// Vercel Hobby only retains runtime logs for an hour, so this is not
		// this failure's durable record, but it is what turns "every link now
		// quietly goes to the shared hostname" from a mystery someone notices
		// downstream into something a `runtime-logs`/Sentry search on this
		// route actually surfaces.
		console.error('loadVerifiedDomains: falling back to no domain picker', error);
		return [];
	}
}

// oxlint-disable-next-line sort-keys -- `validateSearch` is declared first, out of alphabetical order, for the same type-inference reason `teams.$teamSlug.links.index.tsx` gives for its own identical placement.
export const Route = createFileRoute('/_authed/teams/$teamSlug/links/new')({
	// Declared before `loader`, the same reason `teams.$teamSlug.links.index.tsx`
	// gives for its own `validateSearch`: moving it later risks the same
	// inference fallback to `{}` for anything downstream that reads this
	// route's own search type. `folder` and `tag` preselect a folder and a tag
	// on the create form (below); an invalid or malformed value is dropped,
	// not surfaced, same as `parseFolderSearch`'s handling of the list route's
	// own filter.
	validateSearch: (
		search: { folder?: unknown; tag?: unknown } & SearchSchemaInput,
	): { folder?: string; tag?: string } => ({
		folder: parseFolderIdSearch(search.folder),
		tag: parseUuidSearch(search.tag),
	}),
	beforeLoad: ({ context, params }) => ({
		// Decides whether the tag picker offers to create a tag, the same way
		// the tags page decides whether to offer its create form.
		role: context.me.memberships.find((membership) => membership.slug === params.teamSlug)?.role,
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	component: RouteComponent,
	loader: async ({ context }) => {
		// Only `domains` is returned: the folders and tags fetches are
		// prefetches, not dependencies this loader's own result carries — see
		// `prefetchFolders`'s own docstring for why the two are not symmetric.
		// The component reads folders and tags back out of the same
		// `['folders', teamId]` and `['tags', teamId]` caches with a plain
		// `useQuery`.
		const [domains] = await Promise.all([
			loadVerifiedDomains(context.queryClient, context.teamId),
			prefetchFolders(context.queryClient, context.teamId),
			prefetchTags(context.queryClient, context.teamId),
		]);
		return domains;
	},
});

/**
 * Turns the form's own value shape into the API's request body. Kept out of
 * `LinkForm` itself so that component stays a plain "here are the values"
 * contract the edit route (Task 11, per the plan's pre-flight scan) can reuse
 * without also inheriting how the create route's mutation is built.
 *
 * An empty `slug`/`expires_at`/`domain_id` becomes `undefined`, not `''`: the
 * API generates a slug when the field is omitted
 * (`CreateLinkInputBodyWritable`'s own doc comment) and defaults to the
 * instance's shared domain when `domain_id` is omitted, and Huma's
 * `expires_at` validation expects either a real timestamp or nothing, never
 * an empty string.
 *
 * Exported so `domain_id`'s mapping is falsifiable directly: nothing in this
 * route renders through a real HTTP layer, so the mutation's `onSubmit`
 * wiring alone can't catch a dropped field here — the picker's own
 * component test only proves `LinkForm` hands back the right values, not
 * that this function forwards them (confirmed by deleting the `domain_id`
 * line below and re-running the suite: nothing failed until this file grew
 * its own test for it).
 *
 * @param values - The form's values, as `LinkForm` hands them back.
 * @returns The API request body, with empty optional fields mapped to `undefined`.
 */
/** The two redirect status codes a link can use; see CLAUDE.md's "301 vs 302" note for why 302 is the default. */
const REDIRECT_PERMANENT = 301;
const REDIRECT_TEMPORARY = 302;

export function toRequestBody(values: LinkFormValues): CreateLinkInputBodyWritable {
	return {
		analytics_enabled: values.analytics_enabled,
		destination_url: values.destination_url,
		domain_id: values.domain_id === '' ? undefined : values.domain_id,
		expires_at: values.expires_at === '' ? undefined : new Date(values.expires_at).toISOString(),
		folder_id: values.folder_id === '' ? undefined : values.folder_id,
		redirect_type:
			values.redirect_type === REDIRECT_PERMANENT ? REDIRECT_PERMANENT : REDIRECT_TEMPORARY,
		slug: values.slug === '' ? undefined : values.slug,
		// Left out when none were chosen: a new link has no tags to clear.
		...(values.tag_ids.length > 0 ? { tag_ids: [...values.tag_ids] } : {}),
	};
}

/**
 * Preselects the folder named by the create route's own `folder` search
 * parameter (`?folder=<id>`, e.g. from the link list's "New link" button
 * while a folder filter is active) — but only when the team actually has
 * that folder. A stale or foreign id (the folder was deleted, or belongs to
 * another team the caller once switched from) must not pin the form onto a
 * value the picker cannot render; falling back to `''` ("No folder") is the
 * same "ignore, don't invent" treatment `parseFolderIdSearch` already gives
 * a malformed value.
 *
 * @param requested - The `folder` search parameter, already validated as a well-formed UUID by `parseFolderIdSearch`.
 * @param folders - The team's own folders, as loaded for the picker.
 * @returns `requested` when the team has that folder, `''` otherwise.
 */
export function initialFolderId(
	requested: string | undefined,
	folders: readonly Readonly<{ id: string; name: string }>[],
): string {
	return requested !== undefined && folders.some((folder) => folder.id === requested)
		? requested
		: '';
}

/**
 * The tag counterpart of `initialFolderId`: preselects the tag named by the
 * `tag` search parameter (`?tag=<id>`, e.g. from the link list's "New link"
 * button while a tag filter is active), but only when the team has that tag,
 * so a stale or foreign id never becomes a chip.
 *
 * @param requested - The `tag` search parameter, already validated as a well-formed UUID by `parseUuidSearch`.
 * @param tags - The team's own tags, as loaded for the picker.
 * @returns `[requested]` when the team has that tag, `[]` otherwise.
 */
export function initialTagIds(
	requested: string | undefined,
	tags: readonly TagOption[],
): readonly string[] {
	return requested !== undefined && tags.some((tag) => tag.id === requested) ? [requested] : [];
}

/**
 * The narrow slices of `QueryClient`/`Router` this needs — same reasoning as
 * `LinksDataSource` in the list route (Task 9): a real object satisfies these
 * structurally, so production code needs no cast, and a test can pass a
 * hand-built fake instead of standing up either one for real.
 */
interface InvalidatableQueryClient {
	readonly invalidateQueries: (
		filters: Readonly<{ queryKey: readonly unknown[] }>,
	) => Promise<void>;
}
interface InvalidatableRouter {
	readonly invalidate: () => Promise<void>;
}

/**
 * Extracted so this task's own explicit rule — "invalidate both the links
 * query key and the router... invalidating only one leaves them disagreeing
 * until the next full navigation" — is a falsifiable property against fakes,
 * rather than something only provable by clicking through a real app. Mirrors
 * `loadLinks`'s extraction in the list route (Task 9) for the same reason.
 *
 * Invalidates the whole `['links', teamId]` prefix, not one exact
 * `['links', teamId, page]` key: a newly created link can land on any page a
 * visitor currently has open (sort order isn't this task's concern), and
 * React Query's `invalidateQueries` already treats a queryKey as a prefix
 * match by default.
 *
 * @param queryClient - The query client to invalidate the links cache on.
 * @param router - The router to invalidate, so its loaders refetch too.
 * @param teamId - The team whose links were just created into.
 */
export async function afterCreate(
	queryClient: InvalidatableQueryClient,
	router: InvalidatableRouter,
	teamId: string,
): Promise<void> {
	await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	await router.invalidate();
}

/**
 * What a member below editor sees in place of the create form: the API
 * refuses `POST /v1/teams/{id}/links` below `EditorScope`, so a form here
 * could only end in a 403 after the visitor has filled it in. A page of its
 * own rather than a redirect, because the address is real and the reader may
 * have followed a link or a bookmark to it — they are told why it is empty
 * and offered the way back, as the audit log does for a member below admin.
 *
 * @param props - The component's props.
 * @param props.teamSlug - The team's slug, for the way back to its links.
 * @returns The explanation and a link back to the list.
 */
function NewLinkForbidden({ teamSlug }: { readonly teamSlug: string }): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<>
			<h1>{t('links.forbiddenTitle')}</h1>
			<p>{t('links.forbiddenBody')}</p>
			<Link params={{ teamSlug }} to="/teams/$teamSlug/links">
				{t('links.backToList')}
			</Link>
		</>
	);
}

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { role } = Route.useRouteContext();

	// The page's mutation and queries all live in `NewLinkPage`, so a member who
	// cannot create mounts none of them, and the hooks stay unconditional.
	if (!canEdit(role)) return <NewLinkForbidden teamSlug={teamSlug} />;

	return <NewLinkPage />;
}

function NewLinkPage(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { role, teamId } = Route.useRouteContext();
	const domains = Route.useLoaderData();
	const search = Route.useSearch();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const [failure, setFailure] = useState<ApiFailure | null>(null);
	// Non-suspense: the loader's own `prefetchFolders` already warmed this
	// cache on the happy path, so this resolves from cache immediately, but a
	// prefetch failure must not take the whole create page down with it — see
	// `prefetchFolders`'s own docstring. `data` stays `undefined` while
	// pending or failed, and `?? []` below is what keeps the select rendering
	// with only "No folder" in either case. This is also what makes the 422
	// "folder gone" refetch (`invalidateQueries(['folders', teamId])` in
	// `onError` below) actually visible: a loader-time snapshot would never
	// update after that refetch resolves.
	const { data: folderPage } = useQuery(foldersQueryOptions(teamId));
	const folders = folderPage?.items ?? [];
	// Non-suspense for the same reason as the folders above; see the hook.
	const { canCreateTags, onCreateTag, tags, tagsLoaded } = useLinkFormTags(teamId, role);

	const mutation = useMutation({
		mutationFn: async (values: LinkFormValues) =>
			createLinkFn({ data: { body: toRequestBody(values), teamId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			// A render can't throw a redirect the way a loader/`beforeLoad` can —
			// see `LinksError`'s docstring in the list route for the same point —
			// and this is further still: an event-handler callback, not even a
			// render. `router.navigate` is the imperative call for exactly that.
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			// A folder deleted between loading this page and submitting: the
			// generic API message ("body.folder_id: ...") means nothing to a
			// Verein board member, and the stale entry in `['folders', teamId]`
			// is what would keep offering the gone folder on a retry without the
			// refetch `remapFolderGoneFailure` triggers. A chosen tag deleted
			// meanwhile is the same story on `tag_ids`.
			const folderChecked = remapFolderGoneFailure(classified, {
				folderGoneMessage: t('links.folderGone'),
				queryClient,
				teamId,
			});
			setFailure(
				remapTagGoneFailure(folderChecked, {
					queryClient,
					tagGoneMessage: t('links.tagGone'),
					teamId,
				}),
			);
		},
		onSuccess: async () => {
			setFailure(null);
			// `queryClient`/`router` here are the real instances from React
			// context, satisfying `afterCreate`'s narrower parameter types
			// structurally — no cast needed. Those narrower types are what let
			// the same function also be called with hand-built fakes in the test
			// for this property.
			await afterCreate(queryClient, router, teamId);
			await router.navigate({ params: { teamSlug }, to: '/teams/$teamSlug/links' });
		},
	});

	const fieldErrors = failure?.kind === 'fields' ? failure.fields : undefined;
	// `fields` renders on the form itself, via `fieldErrors` above — a second,
	// generic message here would be the "banner about an error" this task's
	// own rule says a field error must not become.
	const formMessage = failure && failure.kind !== 'fields' ? t(`errors.${failure.kind}`) : null;

	return (
		<>
			<h1>{t('links.create')}</h1>
			{formMessage !== null ? <p role="alert">{formMessage}</p> : null}
			<LinkForm
				canCreateTags={canCreateTags}
				domains={domains}
				fieldErrors={fieldErrors}
				folderHint={
					// oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- one static, one-line link rendered once per page visit; a stable reference would need a `useMemo` around an element that never changes across this component's own re-renders.
					<Link params={{ teamSlug }} to="/teams/$teamSlug/folders">
						{t('links.folderNoneYet')}
					</Link>
				}
				folders={folders}
				initial={{
					folder_id: initialFolderId(search.folder, folders),
					tag_ids: initialTagIds(search.tag, tags),
				}}
				onCreateTag={onCreateTag}
				onSubmit={(values) => {
					mutation.mutate(values);
				}}
				tags={tags}
				tagsLoaded={tagsLoaded}
			/>
		</>
	);
}
