import * as Sentry from '@sentry/tanstackstart-react';

import { classifyApiError } from './api-errors';

/** The complete set of request headers permitted to leave the browser. */
const ALLOWED_HEADERS = new Set(['user-agent']);

/**
 * Substrings of key names `dataCollection`'s deny lists reject below —
 * covers `x-forwarded-for`, `x-real-ip`, `cf-connecting-ip`, `remote-addr`,
 * `via`, `x-forwarded-user`, and their cookie/query-param equivalents.
 * Belt-and-braces alongside `scrubEvent`: this only matters if a header,
 * cookie, or query param reaches Sentry through a path `beforeSend` above
 * does not see.
 */
const IP_OR_USER_LIKE_KEYS = ['forwarded', '-ip', 'remote-', 'via', '-user'];

/**
 * Breadcrumb `data` keys that carry a URL. `@sentry/browser`'s breadcrumbs
 * integration builds `fetch`/`xhr` breadcrumb data as
 * `{ ...fetchData, status_code }` / `{ method, url, status_code }` — both
 * `url` — and navigation breadcrumb data as `{ from, to }`. None of these
 * three go through any sanitization step in the SDK.
 *
 * Swept unconditionally on every breadcrumb rather than gated by
 * `category`: a category allowlist would need to track the SDK's breadcrumb
 * category names across majors (the same reason `console` filtering below
 * matches on data shape, not on the integration that produced it), and
 * these three key names are specific enough that stripping a query string
 * off whatever they hold is safe even on a breadcrumb category that turns
 * out not to carry a URL after all.
 */
const BREADCRUMB_URL_KEYS = ['from', 'to', 'url'] as const;

/** Strips the query string off a URL. Shared by `request.url` and every breadcrumb field that carries a URL. */
function stripQueryString(url: string): string {
	// `split` on a non-empty separator always yields at least one element;
	// the `?? url` only satisfies `noUncheckedIndexedAccess`, it is never hit.
	return url.split('?')[0] ?? url;
}

/**
 * `beforeSend`, and the thing that actually enforces this project's rule
 * about what may leave a visitor's browser. `dataCollection` below reduces
 * what is collected; this guarantees what is sent.
 *
 * Golden rule 5 — never store a full IP address, ever — has a second half
 * that no code can cover: browser events reach Sentry over the visitor's own
 * connection, so Sentry's ingest sees the address regardless. The project
 * setting "Prevent Storing of IP Addresses" is the other switch, and both
 * are required.
 */
export function scrubEvent(event: Sentry.ErrorEvent): Sentry.ErrorEvent {
	// `Sentry.ErrorEvent`'s own `request`/`user`/`breadcrumbs` fields already
	// carry the shapes read and written below, so no cast is needed to reach
	// into them.
	if (event.user) delete event.user.ip_address;

	if (event.breadcrumbs) {
		// Console breadcrumbs carry whatever any code logged. Filtered here
		// rather than by disabling the breadcrumbs integration, so the
		// guarantee survives an SDK major renaming that integration.
		event.breadcrumbs = event.breadcrumbs.filter((crumb) => crumb.category !== 'console');

		for (const crumb of event.breadcrumbs) {
			const { data } = crumb;
			if (!data) continue;

			for (const key of BREADCRUMB_URL_KEYS) {
				// `Breadcrumb.data` is typed `{ [key: string]: any }` by the
				// SDK; the annotation narrows the read to `unknown` so it is
				// checked below instead of trusted.
				const value: unknown = data[key];
				if (typeof value === 'string') data[key] = stripQueryString(value);
			}
		}
	}

	const { request } = event;
	if (request) {
		delete request.cookies;
		delete request.data;
		delete request.query_string;
		if (request.url) request.url = stripQueryString(request.url);
		if (request.headers) {
			request.headers = Object.fromEntries(
				Object.entries(request.headers).filter(([name]) => ALLOWED_HEADERS.has(name.toLowerCase())),
			);
		}
	}

	return event;
}

/**
 * `classifyApiError` names every failure this app deliberately renders as
 * UI. `unknown` is what is left: a 500, a network failure, a render error —
 * the things nobody chose to handle, and the only things worth an event.
 */
export function isReportable(error: unknown): boolean {
	return classifyApiError(error).kind === 'unknown';
}

/**
 * Errors already reported. A router error component can render more than
 * once for one error, and on the server it renders again on the client after
 * hydration — without this, one failure becomes several events out of the
 * monthly 5,000.
 */
const reported = new WeakSet();

export function reportUnexpected(error: unknown): void {
	if (!isReportable(error)) return;

	if (typeof error === 'object' && error !== null) {
		if (reported.has(error)) return;
		reported.add(error);
	}

	Sentry.captureException(error);
}

/**
 * `dataCollection` is the v10 replacement for the deprecated
 * `sendDefaultPii: false`. It is defence in depth next to `scrubEvent`, not
 * a substitute for it.
 *
 * No tracing and no replay: `tracesSampleRate` stays unset, and replay would
 * be PII capture by design.
 *
 * `@sentry/core`'s `resolveDataCollectionOptions` falls back to its own
 * permissive `DEFAULTS` — not the `sendDefaultPii: false` off-state — for
 * every field this object does not set, the instant `dataCollection` is
 * present at all (`options.dataCollection != null ? DEFAULTS : …`). A
 * partial object here does not narrow collection, it silently widens
 * whatever it leaves out — `databaseQueryData` defaults to `true`, and
 * `@sentry/core` ships a Supabase integration this app uses, so a field
 * left unset today can start attaching query values and returned rows
 * tomorrow with nobody having touched this file.
 *
 * So every field of `DataCollection` (`@sentry/core`'s
 * `types/datacollection.d.ts`) is set explicitly below, to the value
 * `sendDefaultPii: false` itself resolves to
 * (`defaultPiiToCollectionOptions(false)`) unless a comment says otherwise.
 * The deprecated `queryParams` field is the one omission: `urlQueryParams`
 * below already resolves first (`dc.urlQueryParams ?? dc.queryParams ?? …`),
 * so `queryParams` is never consulted.
 */
export function sentryOptions(dsn: string): Parameters<typeof Sentry.init>[0] {
	return {
		beforeSend: scrubEvent,
		dataCollection: {
			cookies: { deny: IP_OR_USER_LIKE_KEYS },
			// The off-state's `false` (Supabase, Postgres, MySQL, ORMs …). No
			// such integration is in use, but this is the field the DEFAULTS
			// fallback would otherwise flip to `true` unnoticed.
			databaseQueryData: false,
			// The type's own `@default 5` is stale — both the on- and
			// off-state actually resolve this to 7 (a comment in
			// `defaultPiiToCollectionOptions` notes the mismatch); matching
			// the off-state's real value, not its doc comment.
			frameContextLines: 7,
			genAI: { inputs: false, outputs: false },
			// The off-state's `true`: the SDK redacts literal values out of
			// the GraphQL document at collection time, so this was always
			// sent regardless of `sendDefaultPii`. No GraphQL integration is
			// enabled here either way.
			graphQL: { document: true, variables: true },
			httpBodies: [],
			httpHeaders: {
				request: { deny: IP_OR_USER_LIKE_KEYS },
				response: { deny: IP_OR_USER_LIKE_KEYS },
			},
			// The off-state's `true`: stack-frame local variables are not
			// gated by `sendDefaultPii` either. Left matching the off-state
			// rather than narrowed further, since that narrowing belongs to
			// a decision about this field specifically, not to closing the
			// DEFAULTS trap this fix targets.
			stackFrameVariables: true,
			urlQueryParams: { deny: IP_OR_USER_LIKE_KEYS },
			userInfo: false,
		},
		dsn,
		// Vercel's own VERCEL_ENV and VERCEL_GIT_COMMIT_SHA exist only at
		// build time and carry no VITE_ prefix, so the browser bundle cannot
		// see them. Step 6 defines these two from them instead of asking the
		// maintainer to duplicate two more variables in the dashboard.
		environment: import.meta.env.VITE_SENTRY_ENVIRONMENT || 'development',
		release: import.meta.env.VITE_SENTRY_RELEASE || undefined,
	};
}

/**
 * `Sentry.init` is process-global, and `getRouter` runs once per request on
 * the server — so this guards against re-initialising the client on every
 * page view.
 */
let initialized = false;

/**
 * Called from `getRouter`, which is the one place that exists in both
 * bundles. Sentry's own documentation prefers an `instrument.server.mjs`
 * loaded with node's `--import`, which this deployment cannot arrange: the
 * server bundle is built by Nitro and run by Vercel, and neither exposes the
 * node command line. With tracing off, plain `Sentry.init` is enough for
 * error capture, which is all this project asked for. If auto-instrumentation
 * is ever wanted, that constraint is what has to be solved first.
 */
export function initSentry(isServer: boolean): void {
	if (initialized) return;

	const dsn = import.meta.env.VITE_SENTRY_DSN;
	if (!dsn) return;

	initialized = true;
	Sentry.init({ ...sentryOptions(dsn), serverName: isServer ? 'web-ssr' : undefined });
}
