import { getHealth } from '@kurze-url/api-client';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { server } from '../test/msw';
import { getApiClient } from './api';

/**
 * The header is read from the environment at client-construction time, so each
 * test sets it before calling getApiClient and restores it afterwards.
 */
afterEach(() => {
	vi.unstubAllEnvs();
});

async function bypassHeaderSentTo(baseUrl: string): Promise<string | null> {
	let seen: string | null = null;
	server.use(
		http.get(`${baseUrl}/v1/health`, ({ request }) => {
			seen = request.headers.get('x-vercel-protection-bypass');
			return HttpResponse.json({ status: 'ok' });
		}),
	);

	await getHealth({ client: getApiClient(baseUrl), throwOnError: true });
	return seen;
}

describe('getApiClient', () => {
	it('sends the API project bypass secret when one is configured', async () => {
		// Without this the call reaches a protected API preview unauthenticated,
		// Vercel answers 302 to its login page, and the probe reports the API
		// unreachable while the API is healthy.
		vi.stubEnv('API_PROTECTION_BYPASS_SECRET', 'a-preview-secret');

		await expect(bypassHeaderSentTo('http://api.test')).resolves.toBe('a-preview-secret');
	});

	it('sends no bypass header when none is configured', async () => {
		// Production is public, so the secret is absent there. Sending an empty
		// or placeholder value would be worse than sending nothing.
		vi.stubEnv('API_PROTECTION_BYPASS_SECRET', '');

		await expect(bypassHeaderSentTo('http://api.test')).resolves.toBeNull();
	});
});
