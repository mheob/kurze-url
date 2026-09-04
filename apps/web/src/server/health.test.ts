import { http, HttpResponse } from 'msw';
import { describe, expect, it } from 'vitest';

import { server } from '../test/msw';
import { fetchHealth } from './health';

describe('fetchHealth', () => {
	it('reports the API status', async () => {
		server.use(http.get('http://api.test/v1/health', () => HttpResponse.json({ status: 'ok' })));

		await expect(fetchHealth('http://api.test')).resolves.toEqual({ status: 'ok' });
	});

	it('reports unreachable rather than throwing', async () => {
		// The page must render even when the API is down. A shell that 500s
		// because a status probe failed would be worse than the probe's absence.
		server.use(http.get('http://api.test/v1/health', () => HttpResponse.error()));

		await expect(fetchHealth('http://api.test')).resolves.toEqual({ status: 'unreachable' });
	});
});
