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
 * A backslash, any control character (C0, DEL and C1), or a lone surrogate. In a `u` regex a valid
 * surrogate pair reads as one code point, so `\p{Cs}` matches only the unpaired half that no URL
 * can carry and that makes `encodeURIComponent` throw a `URIError`: the login would answer 500
 * where it should refuse. The same test as `String.prototype.isWellFormed`, which the app's own
 * `lib` setting does not yet type.
 */
const FORBIDDEN_CHARACTERS = /[\\\p{Cc}\p{Cs}]/u;

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
 * backslash, any control character and an encoded one, and a lone surrogate
 * (which no URL can carry, and which `encodeURIComponent` throws on), and
 * finally resolves the value against a dummy origin and requires that it keeps
 * that origin and the path `/oauth/consent`. A second leading `/` needs no
 * check of its own: the prefix already puts an `o` there.
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
