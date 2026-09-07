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
	}

	const { request } = event;
	if (request) {
		delete request.cookies;
		delete request.data;
		delete request.query_string;
		if (request.url) request.url = request.url.split('?')[0];
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
 * `sendDefaultPii: false`, in the conservative shape Sentry's own options
 * documentation gives for preserving that behaviour. It is defence in depth
 * next to `scrubEvent`, not a substitute for it.
 *
 * No tracing and no replay: `tracesSampleRate` stays unset, and replay would
 * be PII capture by design.
 */
export function sentryOptions(dsn: string): Parameters<typeof Sentry.init>[0] {
	return {
		beforeSend: scrubEvent,
		dataCollection: {
			cookies: { deny: IP_OR_USER_LIKE_KEYS },
			genAI: { inputs: false, outputs: false },
			httpBodies: [],
			httpHeaders: {
				request: { deny: IP_OR_USER_LIKE_KEYS },
				response: { deny: IP_OR_USER_LIKE_KEYS },
			},
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
