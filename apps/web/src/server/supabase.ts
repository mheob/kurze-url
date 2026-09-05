// `process` is ambient-typed here, in this file only, rather than via the root
// tsconfig's `types` field, for the same reason as `server/api.ts`: `types`
// isn't additive, and this is a one-line need in one file.
/// <reference types="node" />

import { createServerClient, type CookieOptions } from '@supabase/ssr';
import type { SupabaseClient } from '@supabase/supabase-js';

/**
 * Auth cookies are not preference cookies. `preferences.ts` writes `lang` and
 * `theme` for the client to read; these must be invisible to JavaScript, which
 * is the whole reason the access token lives here rather than in memory the
 * browser can reach.
 */
export const SUPABASE_COOKIE_OPTIONS: CookieOptions = {
	httpOnly: true,
	sameSite: 'lax',
	secure: true,
	path: '/',
};

// Mirrors the `cookie` package's own `sameSite` mapping (the same package
// @supabase/ssr's `CookieOptions` type is defined against): `true` and
// `'strict'` both serialize to `Strict`; only `'none'` serializes to `None`.
// Anything else — in practice just `'lax'` — serializes to `Lax`. Called only
// when `options.sameSite` is truthy, so `false` never reaches here.
function sameSiteValue(sameSite: NonNullable<CookieOptions['sameSite']>): string {
	if (sameSite === true || sameSite === 'strict') return 'Strict';
	if (sameSite === 'none') return 'None';
	return 'Lax';
}

function serialize(name: string, value: string, options: CookieOptions): string {
	const parts = [`${name}=${value}`, `Path=${options.path ?? '/'}`];
	if (options.maxAge !== undefined) parts.push(`Max-Age=${options.maxAge}`);
	if (options.httpOnly) parts.push('HttpOnly');
	if (options.secure) parts.push('Secure');
	if (options.sameSite) parts.push(`SameSite=${sameSiteValue(options.sameSite)}`);
	return parts.join('; ');
}

function parse(cookieHeader: string | null): { name: string; value: string }[] {
	if (!cookieHeader) return [];

	return cookieHeader
		.split(';')
		.map((part) => part.trim())
		.filter(Boolean)
		.map((part) => {
			const [name, ...rest] = part.split('=');
			return { name: name?.trim() ?? '', value: rest.join('=') };
		})
		.filter((c) => c.name !== '');
}

/**
 * Pure request/response binding, with no shared state of any kind: the only
 * seam @supabase/ssr needs into cookies, factored out of `createSupabase` so
 * it can be exercised directly in tests instead of through a global.
 *
 * @supabase/ssr's own `SetAllCookies` type allows `setAll` to return
 * `Promise<void>`, since a custom cookie store (e.g. Next.js's async cookie
 * jar) may need one. This adapter never awaits anything — `headers.append`
 * and `headers.set` are both synchronous — so `setAll` is typed here as
 * returning plain `void`, a supertype callers may still pass wherever
 * `Promise<void> | void` is expected. Typing it that way, rather than
 * importing @supabase/ssr's own type, is also what keeps a bare call to
 * `setAll(...)` in a test from tripping `no-floating-promises`.
 *
 * The second `setAll` argument — headers @supabase/ssr wants applied to the
 * response alongside the cookies, always `Cache-Control`/`Expires`/`Pragma`
 * in practice, so a CDN or reverse proxy in front of this app never caches a
 * response that sets one person's session cookie for another — is declared
 * optional here only so tests exercising the cookie half alone can call
 * `setAll` with one argument; @supabase/ssr itself always passes it.
 */
export function createCookieAdapter(
	request: Request,
	headers: Headers,
): {
	getAll: () => { name: string; value: string }[];
	setAll: (
		cookies: { name: string; value: string; options: CookieOptions }[],
		responseHeaders?: Record<string, string>,
	) => void;
} {
	return {
		getAll: () => parse(request.headers.get('cookie')),
		setAll: (cookies, responseHeaders) => {
			for (const { name, value, options } of cookies) {
				headers.append(
					'set-cookie',
					serialize(name, value, { ...options, ...SUPABASE_COOKIE_OPTIONS }),
				);
			}
			for (const [key, value] of Object.entries(responseHeaders ?? {})) {
				headers.set(key, value);
			}
		},
	};
}

/**
 * Bound to one request and one outgoing header set. Never module-level: the
 * frontend renders on the server, where one process serves many people, and a
 * shared client would leak one person's session into another's request — the
 * same reason `createApiClient` returns a fresh instance.
 */
export function createSupabase(request: Request, headers: Headers): SupabaseClient {
	const url = process.env.SUPABASE_URL;
	const key = process.env.SUPABASE_PUBLISHABLE_KEY;
	if (!url || !key) {
		throw new Error('SUPABASE_URL and SUPABASE_PUBLISHABLE_KEY are required');
	}

	return createServerClient(url, key, {
		cookies: createCookieAdapter(request, headers),
		cookieOptions: SUPABASE_COOKIE_OPTIONS,
	});
}
