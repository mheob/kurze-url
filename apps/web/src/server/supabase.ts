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

function serialize(name: string, value: string, options: CookieOptions): string {
	const parts = [`${name}=${value}`, `Path=${options.path ?? '/'}`];
	if (options.maxAge !== undefined) parts.push(`Max-Age=${options.maxAge}`);
	if (options.httpOnly) parts.push('HttpOnly');
	if (options.secure) parts.push('Secure');
	if (options.sameSite) parts.push(`SameSite=Lax`);
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

type SetAllCookies = (cookies: { name: string; value: string; options: CookieOptions }[]) => void;

declare global {
	// A real ambient global augmentation, not a cast: `globalThis.x = y` needs
	// `var` here because TypeScript only adds `let`/`const` declarations to
	// module scope, never to the global object, so a block-scoped declaration
	// would leave `__lastSetAll` typed but never actually settable below.
	// oxlint-disable-next-line no-var, no-underscore-dangle
	var __lastSetAll: SetAllCookies | undefined;
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

	const setAll: SetAllCookies = (cookies) => {
		for (const { name, value, options } of cookies) {
			headers.append(
				'set-cookie',
				serialize(name, value, { ...SUPABASE_COOKIE_OPTIONS, ...options }),
			);
		}
	};
	// Exposed for the adapter's own tests: @supabase/ssr only calls setAll
	// during sign-in and refresh, so nothing else in the app would notice a
	// no-op write path.
	// oxlint-disable-next-line no-underscore-dangle
	globalThis.__lastSetAll = setAll;

	return createServerClient(url, key, {
		cookies: { getAll: () => parse(request.headers.get('cookie')), setAll },
	});
}
