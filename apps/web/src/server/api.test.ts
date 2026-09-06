import { getHealth } from '@kurze-url/api-client';
import { http, HttpResponse } from 'msw';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { server } from '../test/msw';
import { apiBaseUrl, getApiClient } from './api';

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

/**
 * The shape Vercel injects as VERCEL_RELATED_PROJECTS. Only the fields
 * withRelatedProject actually reads are set: it takes `production.alias` first
 * for a production deployment and `preview.branch` for a preview one.
 */
function relatedProjects(alias: string, branch: string): string {
	return JSON.stringify([
		{
			preview: { branch },
			production: { alias },
			project: { name: 'kurze-url-api' },
		},
	]);
}

describe('apiBaseUrl', () => {
	it('prefers an explicit API_HOST over the related-projects alias', () => {
		// The Go router serves /v1 on exactly one hostname and treats every other
		// Host header as a short-link domain, so pointing this app at the wrong
		// one does not fail loudly — every call is read as a slug and 404s. That
		// is what happened when the API moved to its custom domain and
		// related-projects went on resolving the *.vercel.app alias, so the
		// explicit setting has to win rather than be a fallback.
		vi.stubEnv('API_HOST', 'https://api.example.test');
		vi.stubEnv('VERCEL_ENV', 'production');
		vi.stubEnv('VERCEL_RELATED_PROJECTS', relatedProjects('stale.vercel.app', 'b.vercel.app'));

		expect(apiBaseUrl()).toBe('https://api.example.test');
	});

	it('still pairs a preview with the matching API preview', () => {
		// API_HOST is set on Production only. Setting it everywhere would send
		// every preview of this app at the production API, which is the whole
		// reason withRelatedProject is here.
		vi.stubEnv('API_HOST', '');
		vi.stubEnv('VERCEL_ENV', 'preview');
		vi.stubEnv(
			'VERCEL_RELATED_PROJECTS',
			relatedProjects('stale.vercel.app', 'api-branch.vercel.app'),
		);

		expect(apiBaseUrl()).toBe('https://api-branch.vercel.app');
	});

	it('falls back to a local API when nothing is configured', () => {
		vi.stubEnv('API_HOST', '');
		vi.stubEnv('VERCEL_ENV', '');
		vi.stubEnv('VERCEL_RELATED_PROJECTS', '');

		expect(apiBaseUrl()).toBe('http://localhost:8080');
	});
});

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
