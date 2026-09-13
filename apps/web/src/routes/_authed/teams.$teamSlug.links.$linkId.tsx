import type { Link, PageLink, UpdateLinkInputBodyWritable } from '@kurze-url/api-client';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createFileRoute, notFound, redirect, useRouter } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ConfirmDelete } from '../../components/confirm-delete';
import { LinkForm, type LinkFormValues } from '../../components/link-form';
import { LinkPasswordCard } from '../../components/link-password-card';
import { LinkQRCard } from '../../components/link-qr-card';
import { Card, CardContent, CardHeader, CardTitle } from '../../components/ui/card';
import { classifyApiError, type ApiFailure, type QrRejectionReason } from '../../lib/api-errors';
import type { LinkPasswordContext, LinkPasswordReason } from '../../lib/link-password';
import {
	deleteLinkFn,
	getLinkFn,
	linkQrDownloadFn,
	linkQrSvgFn,
	removeLinkPasswordFn,
	setLinkPasswordFn,
	updateLinkFn,
} from '../../server/links';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every parameter this rule flags
   below is typed by something this file does not own: TanStack Router's own `beforeLoad`/`loader`
   option shapes, the generated `@kurze-url/api-client` `Link`/`PageLink` types (whose nested arrays
   are mutable and can't be marked readonly from this side of the codegen boundary), the DOM's own
   `Document` (mutable by definition — see `saveQrDownload`'s own docstring for why it stays
   unnarrowed), or `ApiFailure` from `../../lib/api-errors` — out of this lint pass's scope — whose
   `fields` variant nests a plain, mutable `Record<string, string>`. `Readonly<>` is shallow, so none
   of these clears without editing a declaration this file does not own. */

/**
 * The one shape `loadLink` below reaches through — a real `getLinkFn`
 * satisfies this structurally, so the loader needs no cast, and
 * `teams.$teamSlug.links.$linkId.test.ts` can pass a hand-built fake instead of
 * a real server function (which cannot run directly under Vitest — see
 * `server/links.ts`'s docstrings). Same shape `LinksDataSource` uses in the
 * list route (Task 9) for the identical reason.
 */
type LinkFetcher = (options: Readonly<{ data: Readonly<{ linkId: string }> }>) => Promise<Link>;

/**
 * A non-member of the team never reaches this loader at all — `beforeLoad`'s
 * `requireTeamId` throws first. This is the narrower case: a caller who
 * *is* a team member, but whose `linkId` names a link that either doesn't
 * exist or belongs to someone else. `internal/authz` answers that the same
 * way it answers a non-member team, 404 never 403 (see `requireTeamId`'s
 * own docstring), so `classifyApiError`'s `notFound` is what this throws the
 * router's own `notFound()` for — a generic error page here would be exactly
 * the kind of leak `requireTeamId` exists to prevent, just reached by a
 * different door.
 *
 * `unauthenticated` redirects for the same reason `loadLinks` does in the
 * list route: the narrow window where `_authed.tsx`'s own session check
 * passed but the token dies, or is rejected, by the time this route's own
 * fetch runs.
 *
 * @param fetchLink - The server function to fetch through; only needs this narrow shape.
 * @param linkId - The link's id, from the route's own path parameter.
 * @returns The fetched link.
 */
export async function loadLink(fetchLink: LinkFetcher, linkId: string): Promise<Link> {
	try {
		return await fetchLink({ data: { linkId } });
	} catch (error) {
		const classified = classifyApiError(error);
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		if (classified.kind === 'unauthenticated') throw redirect({ to: '/login' });
		// oxlint-disable-next-line typescript/only-throw-error -- same as above: `notFound()` is the router's own signal, not an Error.
		if (classified.kind === 'notFound') throw notFound();
		throw error;
	}
}

/**
 * The `datetime-local` input's own format (`YYYY-MM-DDTHH:mm`, no seconds, no
 * zone) built from the instant's *local* wall-clock components — the same
 * assumption `link-form.new.tsx`'s `toRequestBody` makes on the way in
 * (`new Date(values.expires_at)` parses a zone-less date-time string as
 * local time; see its own docstring). Slicing the UTC `toISOString()` output
 * instead would silently reinterpret UTC components as local ones, shifting
 * the instant by the viewer's UTC offset on every round trip except exactly
 * UTC+0 — the kind of bug that only shows up once a real reviewer isn't
 * sitting in a UTC timezone. Building from `getFullYear`/`getMonth`/etc.
 * keeps both directions symmetric regardless of the runtime's zone.
 *
 * `null` (no expiry) becomes `''`, not an epoch date — `new Date(null)` is
 * the Unix epoch, and showing that in the input would read as "this link
 * expires January 1970" for a link that never expires at all.
 *
 * @param value - The date/time component to zero-pad.
 * @returns `value`, zero-padded to at least two digits.
 */
function pad(value: number): string {
	return String(value).padStart(2, '0');
}

export function toDateTimeLocal(expiresAt: string | null): string {
	if (expiresAt === null) return '';

	const date = new Date(expiresAt);
	if (Number.isNaN(date.getTime())) return '';

	const year = date.getFullYear();
	const month = pad(date.getMonth() + 1);
	const day = pad(date.getDate());
	const hours = pad(date.getHours());
	const minutes = pad(date.getMinutes());
	return `${year}-${month}-${day}T${hours}:${minutes}`;
}

/**
 * Seeds `<LinkForm initial>` from the fetched `Link` — a `Pick`, not a
 * spread, so the extra fields `Link` carries (`id`, `state`, `tags`, …)
 * never reach `LinkFormValues` and trip an excess-property error.
 *
 * `domain_id` is carried through even though this route passes no `domains`
 * list to `<LinkForm>` (so the picker never renders here, Task 14's own
 * scope stops at the create route) — `Link.domain_id` is always a concrete
 * id, never `''`, and `LinkFormValues` requires the field regardless of
 * whether the picker is shown.
 *
 * @param link - The fetched link to seed the form from.
 * @returns The form's initial values.
 */
function toFormValues(link: Link): LinkFormValues {
	return {
		analytics_enabled: link.analytics_enabled,
		destination_url: link.destination_url,
		domain_id: link.domain_id,
		expires_at: toDateTimeLocal(link.expires_at),
		redirect_type: link.redirect_type,
		slug: link.slug,
	};
}

/**
 * The update counterpart of `link.new.tsx`'s `toRequestBody`. Same empty
 * string → `undefined` treatment for the same reason: Huma's `expires_at`
 * validation wants either a real timestamp or nothing, never `''`, and an
 * empty `slug` here means "don't change it" (there is no dedicated
 * clear-and-regenerate signal in `UpdateLinkInputBodyWritable`, unlike
 * `folder_id`'s explicit `null`-to-unfile) rather than the create form's
 * "generate one" — inherited from reusing the same `<LinkForm>` unmodified.
 *
 * @param values - The form's values, as `LinkForm` hands them back.
 * @returns The API request body, with empty optional fields mapped to `undefined`.
 */
/** The two redirect status codes a link can use; see CLAUDE.md's "301 vs 302" note for why 302 is the default. */
const REDIRECT_PERMANENT = 301;
const REDIRECT_TEMPORARY = 302;

function toUpdateBody(values: LinkFormValues): UpdateLinkInputBodyWritable {
	return {
		analytics_enabled: values.analytics_enabled,
		destination_url: values.destination_url,
		expires_at: values.expires_at === '' ? undefined : new Date(values.expires_at).toISOString(),
		redirect_type:
			values.redirect_type === REDIRECT_PERMANENT ? REDIRECT_PERMANENT : REDIRECT_TEMPORARY,
		slug: values.slug === '' ? undefined : values.slug,
	};
}

/** Same narrow slices as `link.new.tsx`'s `InvalidatableQueryClient`/`InvalidatableRouter` — real instances satisfy these structurally, fakes satisfy them for the test. */
interface InvalidatableQueryClient {
	readonly invalidateQueries: (
		filters: Readonly<{ queryKey: readonly unknown[] }>,
	) => Promise<void>;
}
interface InvalidatableRouter {
	readonly invalidate: () => Promise<void>;
}

/**
 * Shared by both mutations below: the loader owns the list's data, React
 * Query's cache holds this link's own — invalidating only one leaves them
 * disagreeing until the next full navigation, the same property
 * `link.new.tsx`'s `afterCreate` falsifies for creation. Delete's own
 * "navigate back to the list" step lives in its `onSuccess`, not here, since
 * update has no such step.
 *
 * @param queryClient - The query client to invalidate this link's cached list entries on.
 * @param router - The router to invalidate, so its loaders refetch too.
 * @param teamId - The team this link belongs to.
 */
export async function afterMutation(
	queryClient: InvalidatableQueryClient,
	router: InvalidatableRouter,
	teamId: string,
): Promise<void> {
	await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	await router.invalidate();
}

export const Route = createFileRoute('/_authed/teams/$teamSlug/links/$linkId')({
	beforeLoad: ({ context, params }) => ({
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	component: RouteComponent,
	loader: async ({ params }) => loadLink(getLinkFn, params.linkId),
});

/**
 * The membership list `_authed.tsx` already fetched — this route's own
 * `beforeLoad` resolves `teamId` against the identical list (`requireTeamId`),
 * so reading `name` back out of it here costs no request. An empty
 * `teamName` is unreachable: `beforeLoad` has already thrown `notFound()` for
 * a `teamSlug` with no matching membership, and the `?? ''` only exists so
 * the type is `string` without a non-null assertion.
 *
 * Exported (like `loadLink`/`afterMutation` above) so
 * `teams.$teamSlug.links.$linkId.test.ts` can exercise it with a hand-built
 * `memberships` array, no router or React tree required.
 *
 * @param link - The fetched link the password context is built for.
 * @param memberships - The signed-in caller's own membership list, from `GET /v1/me`.
 * @param teamSlug - The team slug from the route's path parameter, looked up in `memberships`.
 * @returns The context `LinkPasswordCard` needs to explain its policy.
 */
export function toPasswordContext(
	link: Link,
	memberships: readonly Readonly<{ name: string; slug: string }>[],
	teamSlug: string,
): LinkPasswordContext {
	const membership = memberships.find((candidate) => candidate.slug === teamSlug);
	return {
		destinationUrl: link.destination_url,
		linkSlug: link.slug,
		teamName: membership?.name ?? '',
		teamSlug,
	};
}

/**
 * The one `queryClient` method `applyPasswordSuccess` needs, narrowed the
 * same way `InvalidatableQueryClient` above narrows `invalidateQueries` — a
 * real `QueryClient` satisfies this structurally, and a hand-built fake can
 * satisfy it for the test without constructing one.
 */
interface CacheWritableQueryClient {
	readonly setQueriesData: (
		filters: Readonly<{ exact: boolean; queryKey: readonly unknown[] }>,
		updater: (old: PageLink | undefined) => PageLink | undefined,
	) => unknown;
}

/** Dependencies `handlePasswordError` needs from the component, narrowed to exactly the calls it makes — see `CacheWritableQueryClient` above for why this shape, not the real hooks, is what gets threaded through. */
interface PasswordErrorHandlers {
	readonly navigateToLogin: () => void;
	readonly setFailure: (failure: ApiFailure | null) => void;
	readonly setPasswordRejection: (reason: LinkPasswordReason | 'rejected' | undefined) => void;
}

/**
 * Shared by both password mutations: a policy rejection (Task 8's
 * `passwordRejected`) goes to the card's own `rejection` prop, never the
 * page banner — `<LinkPasswordCard>` already renders it next to the field
 * it belongs to. Every other failure kind (rate limited, not found, a
 * genuine 500) falls through to the same `failure` state the form and
 * delete mutations already use, so it renders through the one banner this
 * route has rather than a second, parallel one.
 *
 * Exported and taking its dependencies as an explicit parameter, rather than
 * closing over the component's hooks, is what makes this the layer the
 * task-9 review found untested: `teams.$teamSlug.links.$linkId.test.ts` can
 * call this directly with hand-built spies, the same pattern `afterMutation`
 * already uses above.
 *
 * @param error - The value caught from a rejected password mutation.
 * @param handlers - The component callbacks this dispatches to, based on the error's kind.
 */
export function handlePasswordError(error: unknown, handlers: PasswordErrorHandlers): void {
	const classified = classifyApiError(error);
	if (classified.kind === 'unauthenticated') {
		handlers.navigateToLogin();
		return;
	}
	if (classified.kind === 'passwordRejected') {
		handlers.setFailure(null);
		handlers.setPasswordRejection(classified.reason);
		return;
	}
	handlers.setPasswordRejection(undefined);
	handlers.setFailure(classified);
}

/** Dependencies `applyPasswordSuccess` needs from the component — see `PasswordErrorHandlers` above for the same reasoning. */
interface PasswordSuccessHandlers {
	readonly linkId: string;
	readonly queryClient: CacheWritableQueryClient;
	readonly setFailure: (failure: ApiFailure | null) => void;
	readonly setHasPassword: (hasPassword: boolean) => void;
	readonly setPasswordRejection: (reason: LinkPasswordReason | 'rejected' | undefined) => void;
	readonly teamId: string;
}

/**
 * Writes the returned `Link` straight into the cached link list
 * (`['links', teamId, page]`, whichever pages happen to be cached — the
 * page number isn't known here) so the lock badge (Task 9, `LinkList`)
 * reflects the change without waiting on a refetch, the same reasoning
 * `teams.$teamSlug.domains.tsx`'s verify mutation merges one row into its
 * own cached list instead of invalidating it. `hasPassword` is this same
 * value, tracked locally because it is what this page's own card reads.
 *
 * @param updatedLink - The link returned by the password mutation that just succeeded.
 * @param handlers - The component state and query client this writes the result into.
 */
export function applyPasswordSuccess(updatedLink: Link, handlers: PasswordSuccessHandlers): void {
	handlers.setHasPassword(updatedLink.has_password);
	handlers.setPasswordRejection(undefined);
	handlers.setFailure(null);
	handlers.queryClient.setQueriesData(
		{ exact: false, queryKey: ['links', handlers.teamId] },
		(old) =>
			old
				? {
						...old,
						items: (old.items ?? []).map((item) =>
							item.id === handlers.linkId ? updatedLink : item,
						),
					}
				: old,
	);
}

/** Dependencies `handleQrError` needs from the component — see `PasswordErrorHandlers` above for why the dependencies are a parameter rather than a closure. */
interface QrErrorHandlers {
	readonly navigateToLogin: () => void;
	readonly setFailure: (failure: ApiFailure | null) => void;
	readonly setQrRejection: (reason: QrRejectionReason | 'rejected' | undefined) => void;
}

/**
 * The QR counterpart of `handlePasswordError`, and the same split: a refusal
 * the endpoint keyed to one of its own query parameters belongs under the
 * control that caused it, and everything else — a rate limit, a 404, a
 * genuine 500 — falls through to the one banner this route already has.
 *
 * @param error - The value caught from a rejected QR download mutation.
 * @param handlers - The component callbacks this dispatches to, based on the error's kind.
 */
export function handleQrError(error: unknown, handlers: QrErrorHandlers): void {
	const classified = classifyApiError(error);
	if (classified.kind === 'unauthenticated') {
		handlers.navigateToLogin();
		return;
	}
	if (classified.kind === 'qrRejected') {
		handlers.setFailure(null);
		handlers.setQrRejection(classified.reason);
		return;
	}
	handlers.setQrRejection(undefined);
	handlers.setFailure(classified);
}

/**
 * Turns the base64 a QR download arrives as back into a file the browser
 * saves.
 *
 * The bytes are encoded because a server function's return value is
 * serialised and a `Uint8Array` does not survive that (see
 * `linkQrDownloadFor` in `server/links.ts`). `doc` is a parameter rather than
 * the global so the route's test can drive this with a stub instead of a real
 * click, the same reasoning every other exported helper in this file follows.
 *
 * NOTE (task-9 review, finding 1): this was meant to take a narrowed
 * `DownloadDocument`/`DownloadAnchor` pair instead of the full `Document`, the
 * same way every other injected dependency in this file is narrowed. That
 * narrowing is blocked — `Node.appendChild`/`removeChild`'s own generic
 * signature (`<T extends Node>(node: T): T`) makes it type-theoretically
 * impossible for a plain, non-`Node` anchor shape to satisfy both the real
 * `document` and a hand-built test double at once, without an unsafe
 * assertion somewhere (see the task-9 fix report's escalation for the full
 * argument). Settled, not just deferred: `doc` stays `Document`, and the
 * test drives this with the real jsdom `document` plus `vi.spyOn` rather
 * than a hand-built double, so no cast or suppression is needed on either
 * side — see the test file's own comment above `spyOnDownloadAnchor`.
 *
 * The object URL is revoked immediately: the click has already started the
 * save, and leaving it alive would pin the whole image in memory for the life
 * of the document.
 *
 * @param download - The base64-encoded image bytes and their content type.
 * @param filename - The name to save the download under.
 * @param doc - The document to create and click a throwaway download anchor in.
 */
export function saveQrDownload(
	download: Readonly<{ base64: string; contentType: string }>,
	filename: string,
	doc: Document,
): void {
	const binary = atob(download.base64);
	const bytes = Uint8Array.from(binary, (character) => character.codePointAt(0) ?? 0);
	const url = URL.createObjectURL(new Blob([bytes], { type: download.contentType }));

	const anchor = doc.createElement('a');
	anchor.download = filename;
	anchor.href = url;
	anchor.rel = 'noopener';
	// oxlint-disable-next-line unicorn/prefer-dom-node-append -- `append()` returns nothing where `appendChild()` returns the node, and `teams.$teamSlug.links.$linkId.test.ts`'s `spyOnDownloadAnchor` asserts through a spy on `appendChild` specifically.
	doc.body.appendChild(anchor);
	anchor.click();
	// oxlint-disable-next-line unicorn/prefer-dom-node-remove -- same reason as `appendChild` above: the test spies on `removeChild` by name, and `.remove()` is a different call it would not see.
	doc.body.removeChild(anchor);
	URL.revokeObjectURL(url);
}

/**
 * The two channels `completeQrDownload` can still touch once the mutation has
 * already succeeded — a subset of `QrErrorHandlers`, minus `navigateToLogin`,
 * which a save can never need — plus the document it renders a throwaway
 * download anchor into. `doc` joined this interface (rather than staying its
 * own parameter) to bring `completeQrDownload` back under `max-params`' limit
 * of three: `download` and `filename` are the save's own subject, and
 * everything else it touches is a dependency, so grouping the dependencies is
 * the split that matches what each parameter *is*, not just a count reduction.
 */
interface QrDownloadDeps {
	readonly doc: Document;
	readonly setFailure: (failure: ApiFailure | null) => void;
	readonly setQrRejection: (reason: QrRejectionReason | 'rejected' | undefined) => void;
}

/**
 * Runs once `onDownload`'s mutation has already succeeded: clears both error
 * channels, then guards the save itself.
 *
 * Task-9 review finding 2: `saveQrDownload` can still throw — a malformed
 * `atob` decode, a blocked `URL.createObjectURL`, any DOM exception — and that
 * failure is not a QR refusal. Both channels were just cleared, so without a
 * guard here the error would propagate into `LinkQRCard`'s own bare
 * `catch {}`, whose comment assumes the parent already classified the failure
 * and fed a reason back through `rejection` — true for a failed mutation, not
 * for a failed save, so nothing would render at all. Routing it to
 * `setFailure` instead puts it in the one banner this route already has, the
 * same `{ kind: 'unknown' }` `classifyApiError` falls back to for anything it
 * cannot classify — a DOM exception being exactly that.
 *
 * Extracted as its own exported function, taking `doc` and its handlers as
 * parameters rather than closing over the component's hooks, for the same
 * reason every other exported helper in this file is: so the route's test can
 * drive the guard directly, without a router or a rendered tree.
 *
 * @param download - The base64-encoded image bytes and their content type.
 * @param filename - The name to save the download under.
 * @param deps - The document to render the throwaway anchor in, plus the component's error channels, cleared on entry and set if the save throws.
 */
export function completeQrDownload(
	download: Readonly<{ base64: string; contentType: string }>,
	filename: string,
	deps: QrDownloadDeps,
): void {
	deps.setQrRejection(undefined);
	deps.setFailure(null);
	try {
		saveQrDownload(download, filename, deps.doc);
	} catch {
		deps.setFailure({ kind: 'unknown' });
	}
}

function RouteComponent(): React.JSX.Element {
	const { linkId, teamSlug } = Route.useParams();
	const { me, teamId } = Route.useRouteContext();
	const link = Route.useLoaderData();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const [failure, setFailure] = useState<ApiFailure | null>(null);
	// `has_password` is write-only from the API's point of view (Task 7/8's
	// own reasoning): the only way this page learns it changed is the value a
	// successful `set`/`removeLinkPassword` call hands back, so it is tracked
	// here rather than re-derived from `link` on every render.
	const [hasPassword, setHasPassword] = useState(link.has_password);
	const [passwordRejection, setPasswordRejection] = useState<
		LinkPasswordReason | 'rejected' | undefined
	>();
	const [qrRejection, setQrRejection] = useState<QrRejectionReason | 'rejected' | undefined>();

	// One fetch per link, for the whole life of the card, refetched only when
	// the matrix itself could differ. The matrix depends on the slug and the
	// hostname, so the slug is in the key below — `<LinkForm>` on this same
	// route lets the slug change, and `afterMutation`'s `router.invalidate()`
	// refreshes `link.slug` from the loader but never touches this query, so
	// without the slug in the key a rename would keep showing the *old*
	// short URL's matrix while Download (a fresh fetch) already serves the
	// new one. The hostname is deliberately not in the key: this route
	// passes no `domains` list to `<LinkForm>`, and `toUpdateBody` omits
	// `domain_id`, so a link's domain cannot change from this page — that is
	// what makes its absence here safe, not an oversight. Colour and size
	// must never join the key either: neither changes the matrix, the card
	// only restyles one fetched document locally (see `LinkQRCard`), and
	// keying on them would refetch on every colour tweak and blow through
	// the 30-request-per-minute limit this feature introduces. `staleTime:
	// Infinity` rests on that same invariant: nothing in the key changes
	// without a navigation this route already handles.
	const qrQuery = useQuery({
		queryFn: async () => linkQrSvgFn({ data: { linkId } }),
		queryKey: ['link-qr', linkId, link.slug],
		staleTime: Number.POSITIVE_INFINITY,
	});

	const qrDownloadMutation = useMutation({
		mutationFn: async (
			options: Readonly<{
				background: string;
				foreground: string;
				format: 'png' | 'svg';
				size: number;
			}>,
		) => linkQrDownloadFn({ data: { ...options, linkId } }),
		onError: (error: unknown) => {
			handleQrError(error, {
				navigateToLogin: () => {
					void router.navigate({ to: '/login' });
				},
				setFailure,
				setQrRejection,
			});
		},
	});

	const updateMutation = useMutation({
		mutationFn: async (values: LinkFormValues) =>
			updateLinkFn({ data: { body: toUpdateBody(values), linkId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			// See `link.new.tsx`'s identical branch: a render can't throw a
			// redirect, and this is further still, an event-handler callback.
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setFailure(classified);
		},
		onSuccess: async () => {
			setFailure(null);
			await afterMutation(queryClient, router, teamId);
		},
	});

	const deleteMutation = useMutation({
		mutationFn: async () => deleteLinkFn({ data: { linkId } }),
		onError: (error: unknown) => {
			const classified = classifyApiError(error);
			if (classified.kind === 'unauthenticated') {
				void router.navigate({ to: '/login' });
				return;
			}
			setFailure(classified);
		},
		onSuccess: async () => {
			setFailure(null);
			await afterMutation(queryClient, router, teamId);
			// Nothing restores a deleted link — navigate back to the list rather
			// than leaving this page rendering a link that no longer exists.
			await router.navigate({ params: { teamSlug }, to: '/teams/$teamSlug/links' });
		},
	});

	function onPasswordError(error: unknown): void {
		handlePasswordError(error, {
			navigateToLogin: () => {
				void router.navigate({ to: '/login' });
			},
			setFailure,
			setPasswordRejection,
		});
	}

	function onPasswordSuccess(updatedLink: Link): void {
		applyPasswordSuccess(updatedLink, {
			linkId,
			queryClient,
			setFailure,
			setHasPassword,
			setPasswordRejection,
			teamId,
		});
	}

	const setPasswordMutation = useMutation({
		mutationFn: async (password: string) => setLinkPasswordFn({ data: { linkId, password } }),
		onError: onPasswordError,
		onSuccess: onPasswordSuccess,
	});

	const removePasswordMutation = useMutation({
		mutationFn: async () => removeLinkPasswordFn({ data: { linkId } }),
		onError: onPasswordError,
		onSuccess: onPasswordSuccess,
	});

	const fieldErrors = failure?.kind === 'fields' ? failure.fields : undefined;
	const formMessage = failure && failure.kind !== 'fields' ? t(`errors.${failure.kind}`) : null;

	return (
		<>
			<h1>{t('links.edit')}</h1>
			{formMessage !== null ? <p role="alert">{formMessage}</p> : null}
			{/*
			 * Each card below remounts when the link changes, so each carries the
			 * link id in its key — but the keys must also differ from *each other*.
			 * React matches children by key among siblings, and three siblings
			 * sharing one key is undefined behaviour: the banner above toggling
			 * between an element and `null` shifts the child list, React mis-maps
			 * the duplicates, and the form and the password card end up rendered
			 * twice in the DOM. That is not theoretical — it duplicated the whole
			 * page on the preview and broke the password e2e spec, which then
			 * matched two password inputs. Prefix every key here.
			 */}
			<Card>
				<CardHeader>
					{/* `CardTitle` hardcodes a `<div>` — see the same note on
					    `link-password-card.tsx`'s `CardTitle`: a real nested `<h2>`
					    keeps this section in the page's heading structure without
					    fighting `jsx-a11y/prefer-tag-over-role`, and without editing
					    `ui/card.tsx`. */}
					<CardTitle>
						<h2>{t('links.detailsHeading')}</h2>
					</CardTitle>
				</CardHeader>
				<CardContent>
					<LinkForm
						fieldErrors={fieldErrors}
						initial={toFormValues(link)}
						key={`form-${linkId}`}
						onSubmit={(values) => {
							updateMutation.mutate(values);
						}}
					/>
				</CardContent>
			</Card>
			<LinkPasswordCard
				context={toPasswordContext(link, me.memberships, teamSlug)}
				hasPassword={hasPassword}
				key={`password-${linkId}`}
				onDismissRejection={() => {
					setPasswordRejection(undefined);
				}}
				onRemove={() => {
					removePasswordMutation.mutate();
				}}
				onSet={async (password) => {
					// `mutateAsync`, not `mutate`: the card awaits this to know
					// whether to close the editor and clear the field (see
					// `LinkPasswordCard`'s own `onSet` docstring) — `onError`/
					// `onSuccess` above still run first, either way.
					await setPasswordMutation.mutateAsync(password);
				}}
				rejection={passwordRejection}
			/>
			<LinkQRCard
				isLoading={qrQuery.isPending}
				key={`qr-${linkId}`}
				onDismissRejection={() => {
					setQrRejection(undefined);
				}}
				onDownload={async (options) => {
					const download = await qrDownloadMutation.mutateAsync(options);
					completeQrDownload(download, `${link.slug}.${options.format}`, {
						doc: document,
						setFailure,
						setQrRejection,
					});
				}}
				rejection={qrRejection}
				svg={qrQuery.data}
			/>
			<Card>
				<CardHeader>
					{/* Same `CardTitle`/`<h2>` note as the details card above. */}
					<CardTitle>
						<h2>{t('links.deleteHeading')}</h2>
					</CardTitle>
				</CardHeader>
				<CardContent>
					<ConfirmDelete
						label={t('links.delete')}
						onConfirm={() => {
							deleteMutation.mutate();
						}}
						question={t('links.deleteQuestion')}
					/>
				</CardContent>
			</Card>
		</>
	);
}
