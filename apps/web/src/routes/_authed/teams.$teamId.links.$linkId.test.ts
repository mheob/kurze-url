import type { Link } from '@kurze-url/api-client';
import { isNotFound, isRedirect } from '@tanstack/react-router';
import { describe, expect, it, vi } from 'vitest';

import { afterMutation, loadLink, toDateTimeLocal } from './teams.$teamId.links.$linkId';

function link(overrides: Partial<Link> = {}): Link {
	return {
		analytics_enabled: true,
		created_at: '2026-01-01T00:00:00.000Z',
		created_by: 'user-a',
		destination_url: 'https://example.org',
		domain_id: 'domain-a',
		expires_at: null,
		folder_id: 'folder-a',
		has_password: false,
		hostname: 'kurze.url',
		id: 'link-a',
		redirect_type: 302,
		short_url: 'https://kurze.url/abc123',
		slug: 'abc123',
		state: 'active',
		tags: [],
		team_id: 'team-a',
		updated_at: '2026-01-01T00:00:00.000Z',
		...overrides,
	};
}

/**
 * Same reasoning as `teams.$teamId.links.index.test.ts`'s identical helper:
 * asserting on a returned value, unconditionally, instead of inside a
 * try/catch — `no-conditional-expect` is error-level, and an `expect` inside
 * `catch` silently skips when nothing throws.
 */
async function rejected(fn: () => Promise<unknown>): Promise<unknown> {
	try {
		await fn();
		return undefined;
	} catch (error) {
		return error;
	}
}

function redirectTarget(error: unknown): string | undefined {
	return isRedirect(error) ? error.options.to : undefined;
}

/** A fetcher that always rejects with a given status — captures `status`, so unlike an inline `() => Promise.reject({ status: 401 })` it isn't flagged as a closure that captures nothing. */
function rejectingWith(status: number): (options: { data: { linkId: string } }) => Promise<Link> {
	return () => Promise.reject({ status });
}

describe('loadLink', () => {
	it('returns the fetched link when the API call succeeds', async () => {
		const data = link();
		const fetchLink = async (): Promise<Link> => data;

		await expect(loadLink(fetchLink, 'link-a')).resolves.toBe(data);
	});

	it('redirects to /login when the API answers unauthenticated', async () => {
		const error = await rejected(() => loadLink(rejectingWith(401), 'link-a'));

		expect(isRedirect(error)).toBe(true);
		expect(redirectTarget(error)).toBe('/login');
	});

	/**
	 * The property this task's own instructions call out: a link belonging to
	 * a team the caller isn't in (or that doesn't exist at all) must not be
	 * reachable. `internal/authz` answers both with 404, and
	 * `classifyApiError` folds a 403 into the same `notFound` kind for the
	 * identical reason `assertMembership` throws `notFound()` for a
	 * non-member team — asserting `isNotFound`, not a bare `.toThrow()`, is
	 * what would catch a regression to a generic error page here.
	 */
	it('throws a router not-found, not a generic error, when the API answers not-found', async () => {
		const error = await rejected(() => loadLink(rejectingWith(404), 'link-a'));

		expect(isNotFound(error)).toBe(true);
	});

	it('rethrows any other failure rather than swallowing it', async () => {
		const boom = { status: 500 };
		const fetchLink = (): Promise<Link> => Promise.reject(boom);

		await expect(loadLink(fetchLink, 'link-a')).rejects.toBe(boom);
	});
});

describe('toDateTimeLocal', () => {
	/**
	 * Task 10 sent `datetime-local` → ISO on submit but never tested the
	 * reverse. Slicing the UTC `toISOString()` string directly (rather than
	 * building from the `Date`'s own local getters) would reinterpret UTC
	 * components as local ones — silently correct only for a machine whose
	 * timezone happens to be UTC+0. Round-tripping through both directions,
	 * whatever timezone this test happens to run in, is what would catch
	 * that: `new Date(iso)` and `new Date(toDateTimeLocal(iso))` must name
	 * the same instant.
	 */
	it('round-trips an ISO timestamp through the datetime-local format', () => {
		const iso = '2026-09-10T12:30:00.000Z';

		const local = toDateTimeLocal(iso);

		expect(new Date(local).toISOString()).toBe(iso);
	});

	/**
	 * `new Date(null)` is the Unix epoch — a link with no expiry must show an
	 * empty field, not "expires 1 January 1970".
	 */
	it('leaves the field empty for a link with no expiry', () => {
		expect(toDateTimeLocal(null)).toBe('');
	});
});

describe('afterMutation', () => {
	/**
	 * The loader owns the list's data, the Query cache holds it —
	 * invalidating only one leaves them disagreeing until the next full
	 * navigation, the same property `link.new.tsx`'s `afterCreate` falsifies
	 * for creation. Both update and delete depend on this.
	 */
	it('invalidates both the links query cache and the router', async () => {
		const invalidateQueries = vi.fn(async (): Promise<void> => undefined);
		const invalidate = vi.fn(async (): Promise<void> => undefined);

		await afterMutation({ invalidateQueries }, { invalidate }, 'team-a');

		expect(invalidateQueries).toHaveBeenCalledExactlyOnceWith({ queryKey: ['links', 'team-a'] });
		expect(invalidate).toHaveBeenCalledTimes(1);
	});
});
