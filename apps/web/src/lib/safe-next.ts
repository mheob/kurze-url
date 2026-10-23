const CONSENT_PATH = '/oauth/consent';
const CONSENT_PREFIX = `${CONSENT_PATH}?`;

/**
 * Any origin will do, because it is only ever compared with itself: it is the
 * base a path is resolved against, so a `next` that resolves to a different
 * origin has escaped the path it claimed to be. `.invalid` is reserved and
 * never resolves, which keeps it from standing for a real host by accident.
 */
const DUMMY_ORIGIN = 'https://next.invalid';

/**
 * A backslash, or anything outside printable ASCII (U+0020 to U+007E). Printable ASCII is what the
 * redirect needs: `redirect()` puts `next` in a `Location` header, and `Headers.set` throws a
 * `TypeError` ("Cannot convert argument to a ByteString") for any character above U+00FF, which
 * would fail the auth callback after the session was created. The same rule covers the rest of
 * what a header or a URL must not carry: the C0, DEL and C1 control characters (a tab or newline
 * is stripped by URL parsers and splits a header), U+2028 and the other separators that are not
 * control characters, and a lone surrogate (which also makes `encodeURIComponent` throw a
 * `URIError`). A valid consent link has no use for any of it, since Supabase's authorization IDs
 * are UUIDs and anything else a link carries is percent-encoded.
 *
 * Written as "a control character or a non-ASCII code point", which is the same set as the range
 * `[^\u0020-\u007E]`: the formatter rewrites that range into literal characters and the linter
 * then refuses it as obscure. In a `u` regex each class matches a whole code point, so a surrogate
 * pair (an emoji) is refused as one character.
 */
const FORBIDDEN_CHARACTERS = /[\\\p{Cc}\P{ASCII}]/u;

/**
 * An encoded control character, such as the `%0d%0a` of a header-splitting
 * attempt. Nothing decodes it on the way here, so it is inert today; the guard
 * refuses it anyway, because no consent link has a reason to carry one and the
 * day a consumer decodes `next` once more it would turn into a line break.
 */
const ENCODED_CONTROL_CHARACTER = /%(?:[01][\da-f]|7f)/iu;

/**
 * Decides whether a login's `next` value may be followed after sign-in. This is
 * the open-redirect guard: the spec honours `next` only when it is a same-origin
 * path starting with `/oauth/consent?`, and everything else falls back to `/`.
 *
 * Matching that prefix is necessary and not enough on its own. A URL parser
 * strips tabs and newlines before it reads anything, and treats `\` as `/`, so
 * a value can pass a plain `startsWith` and still be read as another host or
 * smuggle a line break into a header. The guard therefore also refuses a
 * backslash, anything outside printable ASCII (which covers every control
 * character, a lone surrogate, and the characters `redirect()` cannot put in a
 * `Location` header) and an encoded control character, and finally resolves
 * the value against a dummy origin and requires that it keeps that origin and
 * the path `/oauth/consent`. A second leading `/` needs no check of its own:
 * the prefix already puts an `o` there.
 *
 * Case matters (`/OAuth/Consent` is a different path from the route) and so
 * does the query: the bare path is refused because the consent page cannot do
 * anything without an `authorization_id`.
 *
 * @param next - The raw `next` value, which comes from a query string and can
 * be anything, a missing parameter included.
 * @returns Whether `next` is a string that is safe to redirect to.
 */
export function isSafeNext(next: unknown): next is string {
	if (typeof next !== 'string' || !next.startsWith(CONSENT_PREFIX)) {
		return false;
	}

	if (FORBIDDEN_CHARACTERS.test(next) || ENCODED_CONTROL_CHARACTER.test(next)) {
		return false;
	}

	const resolved = new URL(next, DUMMY_ORIGIN);

	return resolved.origin === DUMMY_ORIGIN && resolved.pathname === CONSENT_PATH;
}
