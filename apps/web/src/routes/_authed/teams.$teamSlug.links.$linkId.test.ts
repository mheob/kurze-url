import type { Link, PageLink } from '@kurze-url/api-client';
import { isNotFound, isRedirect } from '@tanstack/react-router';
import { describe, expect, it, vi } from 'vitest';

import {
	afterMutation,
	applyPasswordSuccess,
	handlePasswordError,
	handleQrError,
	loadLink,
	saveQrDownload,
	toDateTimeLocal,
	toPasswordContext,
} from './teams.$teamSlug.links.$linkId';

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
 * Same reasoning as `teams.$teamSlug.links.index.test.ts`'s identical helper:
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
	 * identical reason `requireTeamId` throws `notFound()` for a
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

describe('toPasswordContext', () => {
	it('builds the password context from the link and its own membership', () => {
		const data = link({ destination_url: 'https://example.org/summer', slug: 'sommer' });
		const memberships = [
			{ name: 'Other Verein', slug: 'other' },
			{ name: 'SV Grünwald e.V.', slug: 'sv-gruenwald' },
		];

		expect(toPasswordContext(data, memberships, 'sv-gruenwald')).toEqual({
			destinationUrl: 'https://example.org/summer',
			linkSlug: 'sommer',
			teamName: 'SV Grünwald e.V.',
			teamSlug: 'sv-gruenwald',
		});
	});

	/**
	 * Unreachable in the running app — `beforeLoad` already threw `notFound()`
	 * for a `teamSlug` with no matching membership — but the `?? ''` fallback
	 * exists purely so the type is `string` without a non-null assertion.
	 * Pinned here so that fallback keeps doing what it's for.
	 */
	it('falls back to an empty team name when no membership matches', () => {
		expect(toPasswordContext(link(), [], 'sv-gruenwald').teamName).toBe('');
	});
});

/**
 * `handlePasswordError`'s three-way split is the seam the task-9 review
 * found untested: a `passwordRejected` failure must reach the card's own
 * `rejection` prop, never the page banner, and every other kind must reach
 * the banner instead — mixing those up either drowns a policy message in the
 * generic banner or renders an unrelated failure (rate limited, a genuine
 * 500) as if it were about the password field.
 */
describe('handlePasswordError', () => {
	it('routes a passwordRejected failure into the rejection channel, not the banner', () => {
		const setFailure = vi.fn();
		const setPasswordRejection = vi.fn();
		const navigateToLogin = vi.fn();
		const error = { errors: [{ location: 'body.password', value: 'too_common' }], status: 422 };

		handlePasswordError(error, { navigateToLogin, setFailure, setPasswordRejection });

		expect(setPasswordRejection).toHaveBeenCalledExactlyOnceWith('too_common');
		expect(setFailure).toHaveBeenCalledExactlyOnceWith(null);
		expect(navigateToLogin).not.toHaveBeenCalled();
	});

	it('routes a rate-limited failure into the banner, not the rejection channel', () => {
		const setFailure = vi.fn();
		const setPasswordRejection = vi.fn();
		const navigateToLogin = vi.fn();
		const error = { status: 429 };

		handlePasswordError(error, { navigateToLogin, setFailure, setPasswordRejection });

		expect(setFailure).toHaveBeenCalledExactlyOnceWith({ kind: 'rateLimited' });
		expect(setPasswordRejection).toHaveBeenCalledExactlyOnceWith(undefined);
		expect(navigateToLogin).not.toHaveBeenCalled();
	});

	it('navigates to login for an unauthenticated failure, touching neither state', () => {
		const setFailure = vi.fn();
		const setPasswordRejection = vi.fn();
		const navigateToLogin = vi.fn();
		const error = { status: 401 };

		handlePasswordError(error, { navigateToLogin, setFailure, setPasswordRejection });

		expect(navigateToLogin).toHaveBeenCalledTimes(1);
		expect(setFailure).not.toHaveBeenCalled();
		expect(setPasswordRejection).not.toHaveBeenCalled();
	});
});

/**
 * The lock badge (`LinkList`) reads the cached link list, not a refetch —
 * see this function's own docstring. A fake `queryClient` that implements
 * only `setQueriesData` is what proves that directly: if this ever started
 * invalidating instead of writing through, there would be no
 * `invalidateQueries` here for it to call.
 */
describe('applyPasswordSuccess', () => {
	it('clears failure/rejection state and reports the new hasPassword value', () => {
		const updated = link({ has_password: true });
		const setFailure = vi.fn();
		const setHasPassword = vi.fn();
		const setPasswordRejection = vi.fn();
		const setQueriesData = vi.fn();

		applyPasswordSuccess(updated, {
			linkId: updated.id,
			queryClient: { setQueriesData },
			setFailure,
			setHasPassword,
			setPasswordRejection,
			teamId: 'team-a',
		});

		expect(setHasPassword).toHaveBeenCalledExactlyOnceWith(true);
		expect(setPasswordRejection).toHaveBeenCalledExactlyOnceWith(undefined);
		expect(setFailure).toHaveBeenCalledExactlyOnceWith(null);
		expect(setQueriesData).toHaveBeenCalledExactlyOnceWith(
			{ exact: false, queryKey: ['links', 'team-a'] },
			expect.any(Function),
		);
	});

	it('writes the returned link into an already-cached page in place, leaving others untouched', () => {
		const updated = link({ has_password: true, id: 'link-a' });
		const other = link({ has_password: false, id: 'link-b' });
		let updater: ((old: PageLink | undefined) => PageLink | undefined) | undefined;
		const setQueriesData = vi.fn(
			(_filters: unknown, fn: (old: PageLink | undefined) => PageLink | undefined) => {
				updater = fn;
			},
		);

		applyPasswordSuccess(updated, {
			linkId: 'link-a',
			queryClient: { setQueriesData },
			setFailure: vi.fn(),
			setHasPassword: vi.fn(),
			setPasswordRejection: vi.fn(),
			teamId: 'team-a',
		});

		const page: PageLink = {
			items: [other, link({ has_password: false, id: 'link-a' })],
			page: 1,
			per_page: 100,
			total_count: 2,
		};

		expect(updater?.(page)).toEqual({ ...page, items: [other, updated] });
		expect(updater?.(undefined)).toBeUndefined();
	});
});

describe('handleQrError', () => {
	it('sends an expired session to login', () => {
		const handlers = {
			navigateToLogin: vi.fn(),
			setFailure: vi.fn(),
			setQrRejection: vi.fn(),
		};

		handleQrError({ status: 401 }, handlers);

		expect(handlers.navigateToLogin).toHaveBeenCalledOnce();
		expect(handlers.setFailure).not.toHaveBeenCalled();
	});

	/**
	 * A QR refusal belongs beside the controls that caused it, never in the
	 * page banner — the card already renders it under the colour picker. Same
	 * split `handlePasswordError` makes for a policy rejection.
	 */
	it('routes a QR refusal to the card, not the banner', () => {
		const handlers = {
			navigateToLogin: vi.fn(),
			setFailure: vi.fn(),
			setQrRejection: vi.fn(),
		};

		handleQrError(
			{ errors: [{ location: 'query.fg', message: 'x', value: 'low_contrast' }], status: 422 },
			handlers,
		);

		expect(handlers.setQrRejection).toHaveBeenCalledWith('low_contrast');
		expect(handlers.setFailure).toHaveBeenCalledWith(null);
	});

	it('routes everything else to the page banner', () => {
		const handlers = {
			navigateToLogin: vi.fn(),
			setFailure: vi.fn(),
			setQrRejection: vi.fn(),
		};

		handleQrError({ status: 429 }, handlers);

		expect(handlers.setQrRejection).toHaveBeenCalledWith(undefined);
		expect(handlers.setFailure).toHaveBeenCalledWith({ kind: 'rateLimited' });
	});
});

describe('saveQrDownload', () => {
	/**
	 * The bytes cross the server-function boundary base64-encoded, so the
	 * browser has to rebuild them before it can hand the file to the reader.
	 * Driving it through an injected `Document` is what lets this run without
	 * a router or a real click.
	 */
	it('hands the decoded bytes to the browser under the link’s own name', () => {
		const anchor = { click: vi.fn(), download: '', href: '', rel: '' };
		// A real `Document` has far more required members than `saveQrDownload`
		// ever touches; asserting through `unknown` is what lets this stand-in
		// implement only the three it calls.
		// oxlint-disable-next-line typescript/no-unsafe-type-assertion
		const doc = {
			body: { appendChild: vi.fn(), removeChild: vi.fn() },
			createElement: vi.fn().mockReturnValue(anchor),
		} as unknown as Document;
		const createObjectURL = vi.fn().mockReturnValue('blob:fake');
		const revokeObjectURL = vi.fn();
		// `saveQrDownload` uses nothing else off `URL`, so a two-method stand-in
		// is the whole surface it needs.
		vi.stubGlobal('URL', { createObjectURL, revokeObjectURL });

		saveQrDownload({ base64: btoa('<svg/>'), contentType: 'image/svg+xml' }, 'sommerfest.svg', doc);

		expect(anchor.download).toBe('sommerfest.svg');
		expect(anchor.href).toBe('blob:fake');
		expect(anchor.click).toHaveBeenCalledOnce();
		expect(revokeObjectURL).toHaveBeenCalledWith('blob:fake');

		vi.unstubAllGlobals();
	});
});
