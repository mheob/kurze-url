import {
	createLink,
	deleteLink,
	getLink,
	getLinkQr,
	listLinks,
	removeLinkPassword,
	setLinkPassword,
	updateLink,
	type CreateLinkInputBodyWritable,
	type Link,
	type PageLink,
	type UpdateLinkInputBodyWritable,
} from '@kurze-url/api-client';
import { queryOptions } from '@tanstack/react-query';
import { createServerFn, createServerOnlyFn } from '@tanstack/react-start';
import { getRequest } from '@tanstack/react-start/server';

import { authedApiClient, flushSessionCookies, requireSession } from './session';

/**
 * Takes `request` as a parameter rather than calling `getRequest()` itself,
 * the same shape `requireSession` uses in `server/session.ts`: that is what
 * lets `links.test.ts` call this directly with a synthetic `Request`,
 * instead of needing the server's per-request `AsyncLocalStorage` context
 * that only exists inside a real request (`getRequest()` throws
 * "No Start context found" outside of one, which is exactly what running
 * this under Vitest is).
 *
 * `requireSession` reading the session is itself what refreshes an expiring
 * one, writing new cookies into the `Headers` object threaded through here.
 * `flushSessionCookies` is what carries those onto the real response;
 * skipping it would reproduce, for every page of every team's link list, the
 * exact "login that works and then silently reverts to signed-out" failure
 * `server/auth.ts` and `routes/_authed.tsx` already hit and fixed for
 * sign-in and reading `/v1/me`.
 *
 * Wrapped in `createServerOnlyFn` on the same theory as `sendMagicLinkFor`
 * in `server/auth.ts`: this is a separately exported, by-name-referenced
 * helper (from `listLinksFn`'s handler below) that calls
 * `flushSessionCookies`, which itself reaches `getResponse` from
 * `@tanstack/react-start/server` — conventions.md (from Tasks 2–5) says
 * exactly this shape needs the wrap. Tried the removal to confirm, the same
 * way `sendMagicLinkFor`'s own docstring says it was verified:
 * unlike that case, `pnpm --filter @kurze-url/web build` still passed here
 * with the wrap removed — this file never imports
 * `@tanstack/react-start/server` directly (only `getRequest`, used inline in
 * `listLinksFn`'s handler below, not from inside this function), so
 * Rolldown's import-protection scan apparently doesn't walk through an
 * already-wrapped import (`flushSessionCookies`) to flag a second,
 * un-wrapped caller of it. Left wrapped anyway: it costs nothing (the
 * identity function under Vitest, so `links.test.ts` calling it directly is
 * unaffected), and conventions.md's rule is the documented, defended
 * default for this shape — see task-9-report.md for this divergence.
 */
export const listLinksFor = createServerOnlyFn(
	async (request: Request, teamId: string, page: number): Promise<PageLink> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await listLinks({
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// throwOnError is required: the generated client's default (false)
			// never rejects, so a failed request would resolve to
			// `{ data: undefined, error }` instead of throwing — silently
			// rendering an empty list rather than the loud failure this list is
			// deliberately built to show. `classifyApiError` (Task 8) is written
			// against exactly this thrown shape.
			query: { page, per_page: 20 },
			throwOnError: true,
		});
		return data;
	},
);

/**
 * `getRequest()` (not a `request` field on the handler's context — this
 * version of `createServerFn` has no such field, only `data`/`context`/
 * `method`) reads the incoming request from the server's per-request
 * AsyncLocalStorage, the same correction `server/auth.ts` and
 * `routes/_authed.tsx` already apply. Called inline, here, rather than from
 * inside `listLinksFor`: that keeps `listLinksFor` callable with a synthetic
 * request in tests, the same split `sendMagicLink`/`sendMagicLinkFor` use in
 * `server/auth.ts`.
 */
export const listLinksFn = createServerFn({ method: 'GET' })
	.validator((data: { teamId: string; page: number }) => data)
	.handler(async ({ data }) => listLinksFor(getRequest(), data.teamId, data.page));

/**
 * One definition of the key and the fetcher, used by both the route's loader
 * (`ensureQueryData`) and its component (`useSuspenseQuery`). Two
 * definitions drift, and the symptom is a list that updates on navigation
 * but not after a mutation.
 */
// oxlint's typescript(explicit-function-return-type) is error-level, but
// `queryOptions`'s own return type (`UseQueryOptions<...> &
// QueryKeyWithDataTag<...>`, generic over the query key's own literal tuple
// type) can't be written out by hand without either losing the specific
// `['links', teamId, page]` tuple type `useSuspenseQuery` needs downstream
// or fighting a `ReturnType<typeof queryOptions<PageLink>>` annotation that
// silently widens the key back to `readonly unknown[]` and breaks
// `pnpm typecheck` two call sites away — confirmed by trying it.
// oxlint-disable-next-line typescript/explicit-function-return-type
export const linksQueryOptions = (teamId: string, page: number) =>
	queryOptions({
		queryFn: () => listLinksFn({ data: { teamId, page } }),
		queryKey: ['links', teamId, page] as const,
	});

/**
 * Same `...For`/`...Fn` split as `listLinksFor`/`listLinksFn` above, for the
 * same reason: `createLinkFn`'s `createServerFn` can't be called directly
 * under Vitest ("No Start context found"), so the testable logic takes
 * `request: Request` as a plain parameter instead of reaching for
 * `getRequest()` itself. `links.test.ts` exercises this function directly.
 *
 * `flushSessionCookies` and the `createServerOnlyFn` wrap are both required
 * for the identical reason documented on `listLinksFor`: reading the session
 * via `requireSession` is what refreshes an expiring one, and skipping the
 * flush would silently drop that refresh's cookies on every link creation.
 */
export const createLinkFor = createServerOnlyFn(
	async (request: Request, teamId: string, body: CreateLinkInputBodyWritable): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await createLink({
			body,
			client: authedApiClient(accessToken),
			path: { team_id: teamId },
			// throwOnError is required for the same reason as `listLinksFor`: the
			// generated client's default (false) never rejects, so a validation
			// failure would resolve to `{ data: undefined, error }` instead of
			// throwing — silently reporting success for a link that was never
			// created. `classifyApiError` (Task 8) is written against exactly the
			// thrown shape this produces.
			throwOnError: true,
		});
		return data;
	},
);

/**
 * `getRequest()` called inline here, not inside `createLinkFor`, for the same
 * reason `listLinksFn` does it this way: it keeps `createLinkFor` callable
 * with a synthetic request in tests.
 *
 * `body` is typed as the generated client's own `CreateLinkInputBodyWritable`,
 * not the brief's `Record<string, unknown>` — the route builds one of those
 * from typed `LinkFormValues`, and threading the real type through here means
 * nothing between the route and the API call needs an unsafe cast. Validation
 * of what's actually in the body stays the API's job either way.
 */
export const createLinkFn = createServerFn({ method: 'POST' })
	.validator((data: { body: CreateLinkInputBodyWritable; teamId: string }) => data)
	.handler(async ({ data }) => createLinkFor(getRequest(), data.teamId, data.body));

/**
 * Same `...For`/`...Fn` split, same reason: `getLinkFn`'s `createServerFn` is
 * unreachable under Vitest ("No Start context found"), so
 * `teams.$teamSlug.links.$linkId.test.ts` calls this directly with a synthetic
 * request instead.
 *
 * Not scoped by `team_id` here, deliberately: the API's own entity-scoped
 * authorization (`internal/authz`, per CLAUDE.md) is what decides whether this
 * caller may see this link at all, answering with 404 for a non-member the
 * same way `requireTeamId` does for a whole team. Re-deriving that check
 * here would be a second, divergent copy of a decision the API already makes
 * correctly.
 */
export const getLinkFor = createServerOnlyFn(
	async (request: Request, linkId: string): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await getLink({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
		return data;
	},
);

/** `getRequest()` inline, not inside `getLinkFor`, for the same reason as `listLinksFn`/`createLinkFn`. */
export const getLinkFn = createServerFn({ method: 'GET' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data }) => getLinkFor(getRequest(), data.linkId));

/**
 * Same `...For`/`...Fn` split and the same reasoning as `createLinkFor`.
 * `body` is the generated client's own `UpdateLinkInputBodyWritable` — every
 * field optional, since a PATCH — so the edit route's own transform decides
 * what to send and nothing here needs an unsafe cast.
 */
export const updateLinkFor = createServerOnlyFn(
	async (request: Request, linkId: string, body: UpdateLinkInputBodyWritable): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await updateLink({
			body,
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
		return data;
	},
);

export const updateLinkFn = createServerFn({ method: 'POST' })
	.validator((data: { body: UpdateLinkInputBodyWritable; linkId: string }) => data)
	.handler(async ({ data }) => updateLinkFor(getRequest(), data.linkId, data.body));

/**
 * Same `...For`/`...Fn` split. Returns `void`, not the brief's
 * `{ deleted: true }`: nothing downstream reads a return value — the edit
 * route's mutation only cares whether the promise resolved or rejected — and
 * a shape nothing consumes is just another thing to keep in sync.
 */
export const deleteLinkFor = createServerOnlyFn(
	async (request: Request, linkId: string): Promise<void> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		await deleteLink({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
	},
);

export const deleteLinkFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data }) => deleteLinkFor(getRequest(), data.linkId));

/**
 * Same `...For`/`...Fn` split and the same reasoning as `updateLinkFor`. Both
 * password calls return the whole `Link` rather than nothing, because the API
 * answers with it — the card needs `has_password` back and would otherwise
 * refetch every time.
 */
export const setLinkPasswordFor = createServerOnlyFn(
	async (request: Request, linkId: string, password: string): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await setLinkPassword({
			body: { password },
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
		return data;
	},
);

export const setLinkPasswordFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string; password: string }) => data)
	.handler(async ({ data }) => setLinkPasswordFor(getRequest(), data.linkId, data.password));

export const removeLinkPasswordFor = createServerOnlyFn(
	async (request: Request, linkId: string): Promise<Link> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await removeLinkPassword({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			throwOnError: true,
		});
		return data;
	},
);

export const removeLinkPasswordFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data }) => removeLinkPasswordFor(getRequest(), data.linkId));

/** What `linkQrDownloadFor` hands back: the image, base64-encoded so it survives the server-function boundary, plus the media type to rebuild a `Blob` with. */
export interface QrDownload {
	base64: string;
	contentType: string;
}

/** The colours, format and size one download asks for. */
export interface QrDownloadOptions {
	/** `rrggbb`, no leading `#` — a raw `#` in a query string is the fragment delimiter and would never reach the API. */
	background: string;
	foreground: string;
	format: 'png' | 'svg';
	/** Pixels. Ignored for SVG, and deliberately not sent then: the API answers 422 for a size on an SVG request. */
	size: number;
}

/**
 * Narrows whatever the generated client produced for an image response into
 * bytes.
 *
 * The runtime behaviour is settled: `getParseAs` in the generated client maps
 * any `image/*` content type to `blob`, so this receives a `Blob`. The string
 * branch and the throw exist because the *declared* type is whatever
 * `@hey-api/openapi-ts` chose for a `{type: string, format: binary}` schema,
 * and a regenerated client is free to change that without changing the
 * runtime. Throwing beats defaulting: an empty image is a broken download
 * that reports success.
 */
export async function qrBodyBytes(body: unknown): Promise<Uint8Array> {
	if (body instanceof Blob) return new Uint8Array(await body.arrayBuffer());
	if (typeof body === 'string') return new TextEncoder().encode(body);
	throw new TypeError('unexpected QR response body');
}

/**
 * Fetches the link's QR code once, as SVG in the default colours.
 *
 * The card recolours this document locally for every preview change — neither
 * colour nor size changes the QR matrix, so re-requesting on every drag of a
 * colour picker would spend the endpoint's whole rate limit in seconds for no
 * new information. Two requests per link, not fifty.
 */
export const linkQrSvgFor = createServerOnlyFn(
	async (request: Request, linkId: string): Promise<string> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await getLinkQr({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			query: { format: 'svg' },
			throwOnError: true,
		});
		return new TextDecoder().decode(await qrBodyBytes(data));
	},
);

export const linkQrSvgFn = createServerFn({ method: 'POST' })
	.validator((data: { linkId: string }) => data)
	.handler(async ({ data }) => linkQrSvgFor(getRequest(), data.linkId));

/**
 * The second and last request: the actual download, in the chosen format and
 * colours.
 *
 * `size` is sent only for PNG. A vector has no pixel size, and the API
 * answers 422 rather than ignoring the parameter — the frontend hiding the
 * control is the other half of that, not a replacement for it.
 *
 * The bytes come back base64-encoded because a server function's return value
 * is serialised, and a `Uint8Array` does not survive that intact.
 */
export const linkQrDownloadFor = createServerOnlyFn(
	async (request: Request, linkId: string, options: QrDownloadOptions): Promise<QrDownload> => {
		const headers = new Headers();
		const { accessToken } = await requireSession(request, headers);
		flushSessionCookies(headers);

		const { data } = await getLinkQr({
			client: authedApiClient(accessToken),
			path: { link_id: linkId },
			query: {
				bg: options.background,
				fg: options.foreground,
				format: options.format,
				...(options.format === 'png' ? { size: options.size } : {}),
			},
			throwOnError: true,
		});

		const bytes = await qrBodyBytes(data);
		return {
			base64: Buffer.from(bytes).toString('base64'),
			contentType: options.format === 'png' ? 'image/png' : 'image/svg+xml',
		};
	},
);

export const linkQrDownloadFn = createServerFn({ method: 'POST' })
	.validator((data: QrDownloadOptions & { linkId: string }) => data)
	.handler(async ({ data }) =>
		linkQrDownloadFor(getRequest(), data.linkId, {
			background: data.background,
			foreground: data.foreground,
			format: data.format,
			size: data.size,
		}),
	);
