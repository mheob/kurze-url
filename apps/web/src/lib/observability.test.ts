import type { ErrorEvent } from '@sentry/tanstackstart-react';
import { describe, expect, it } from 'vitest';

import { isReportable, scrubEvent } from './observability';

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
