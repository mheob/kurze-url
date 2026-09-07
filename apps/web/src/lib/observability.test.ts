import type { ErrorEvent } from '@sentry/tanstackstart-react';
import { describe, expect, it } from 'vitest';

import { isReportable, scrubEvent, sentryOptions } from './observability';

/** Every category of request data this project must not send. */
function eventWithEverything(): ErrorEvent {
	return {
		breadcrumbs: [
			{ category: 'console', message: 'user typed a password' },
			{ category: 'fetch', message: 'GET /v1/me' },
		],
		request: {
			cookies: { 'sb-access-token': 'eyJhbGci' },
			data: 'password=hunter2',
			headers: {
				authorization: 'Bearer eyJhbGci',
				cookie: 'sb-access-token=eyJhbGci',
				'user-agent': 'Mozilla/5.0',
				'x-forwarded-for': '203.0.113.7',
			},
			query_string: 'token=secret',
			url: 'https://kurze-url.app/teams/sv-gruenwald/links?token=secret',
		},
		type: undefined,
		user: { ip_address: '203.0.113.7' },
	};
}

describe('scrubEvent', () => {
	it('removes the client address, cookies, body and query', () => {
		const got = scrubEvent(eventWithEverything());

		expect(got.user?.ip_address).toBeUndefined();
		expect(got.request?.cookies).toBeUndefined();
		expect(got.request?.data).toBeUndefined();
		expect(got.request?.query_string).toBeUndefined();
		expect(got.request?.url).toBe('https://kurze-url.app/teams/sv-gruenwald/links');
	});

	it('keeps only the user-agent header', () => {
		const got = scrubEvent(eventWithEverything());

		expect(got.request?.headers).toEqual({ 'user-agent': 'Mozilla/5.0' });
	});

	/**
	 * Console breadcrumbs capture whatever any code logged, which on this app
	 * includes values a person typed. Dropped here rather than by disabling
	 * an integration, so the guarantee does not depend on an integration
	 * name staying stable across SDK majors.
	 */
	it('drops console breadcrumbs and keeps the rest', () => {
		const got = scrubEvent(eventWithEverything());

		expect(got.breadcrumbs).toEqual([{ category: 'fetch', message: 'GET /v1/me' }]);
	});
});

/**
 * `@sentry/browser`'s fetch/xhr/navigation instrumentation attaches a
 * breadcrumb to every event, independent of `tracesSampleRate` — and builds
 * `data.url` (fetch/xhr) or `data.to`/`data.from` (navigation) straight from
 * the raw request, with no sanitization step anywhere in the SDK. A token
 * this app's own fetch calls carry in a query string (an invite or
 * verification token) would otherwise ride along in the breadcrumb trail of
 * whatever error gets reported next.
 */
describe('scrubEvent breadcrumb URLs', () => {
	it('strips the query string from a fetch breadcrumb URL', () => {
		const event: ErrorEvent = {
			breadcrumbs: [
				{
					category: 'fetch',
					data: {
						method: 'GET',
						status_code: 200,
						url: 'https://api.kurze-url.app/v1/teams/1/invite?token=secret',
					},
				},
			],
			type: undefined,
		};

		const got = scrubEvent(event);

		expect(got.breadcrumbs?.[0]?.data?.url).toBe('https://api.kurze-url.app/v1/teams/1/invite');
	});

	it('strips the query string from an xhr breadcrumb URL', () => {
		const event: ErrorEvent = {
			breadcrumbs: [
				{
					category: 'xhr',
					data: {
						method: 'GET',
						status_code: 200,
						url: 'https://api.kurze-url.app/v1/teams/1/invite?token=secret',
					},
				},
			],
			type: undefined,
		};

		const got = scrubEvent(event);

		expect(got.breadcrumbs?.[0]?.data?.url).toBe('https://api.kurze-url.app/v1/teams/1/invite');
	});

	it('strips the query string from navigation breadcrumb from/to', () => {
		const event: ErrorEvent = {
			breadcrumbs: [
				{
					category: 'navigation',
					data: {
						from: '/teams/sv-gruenwald/invite?token=secret',
						to: '/teams/sv-gruenwald/verify?token=secret',
					},
				},
			],
			type: undefined,
		};

		const got = scrubEvent(event);

		expect(got.breadcrumbs?.[0]?.data?.from).toBe('/teams/sv-gruenwald/invite');
		expect(got.breadcrumbs?.[0]?.data?.to).toBe('/teams/sv-gruenwald/verify');
	});
});

describe('isReportable', () => {
	/**
	 * The quota trap. This app renders API failures as UI on purpose —
	 * classifyApiError turns 403, 422 and field errors into copy in two
	 * languages. One Verein mistyping a hostname repeatedly would otherwise
	 * spend the monthly event budget on events carrying nothing the person
	 * was not already shown.
	 */
	it.each([
		['unauthenticated', 401],
		['not found', 404],
		['rate limited', 429],
		['field errors', 422],
	])('does not report an expected %s failure', (_label, status) => {
		expect(isReportable({ errors: [{ location: 'body.hostname', message: 'bad' }], status })).toBe(
			false,
		);
	});

	it('reports a server failure', () => {
		expect(isReportable({ status: 500 })).toBe(true);
	});

	it('reports something that is not an API failure at all', () => {
		expect(isReportable(new TypeError('cannot read properties of undefined'))).toBe(true);
	});
});

describe('sentryOptions', () => {
	/**
	 * `@sentry/core`'s `resolveDataCollectionOptions` falls back to its own
	 * permissive `DEFAULTS` — not the `sendDefaultPii: false` off-state — for
	 * any field a supplied `dataCollection` object does not set. Pinning the
	 * full field list here means adding a field to the SDK's type without
	 * setting it here, or deleting one that is set here, fails this test
	 * instead of silently falling through to "collect".
	 */
	it('sets every DataCollectionOptions field explicitly', () => {
		const { dataCollection } = sentryOptions('https://public@o0.ingest.sentry.io/0');

		// `toEqual` on two `Set`s compares membership, not insertion order —
		// this pins which fields are set, not the order they are written in.
		expect(new Set(Object.keys(dataCollection ?? {}))).toEqual(
			new Set([
				'cookies',
				'databaseQueryData',
				'frameContextLines',
				'genAI',
				'graphQL',
				'httpBodies',
				'httpHeaders',
				'stackFrameVariables',
				'urlQueryParams',
				'userInfo',
			]),
		);
		expect(new Set(Object.keys(dataCollection?.httpHeaders ?? {}))).toEqual(
			new Set(['request', 'response']),
		);
		expect(new Set(Object.keys(dataCollection?.graphQL ?? {}))).toEqual(
			new Set(['document', 'variables']),
		);
		expect(new Set(Object.keys(dataCollection?.genAI ?? {}))).toEqual(
			new Set(['inputs', 'outputs']),
		);
	});
});
