import { classifyApiError, statusOf } from './api-errors';

/** Mirrors the API's shared folder and tag name rule. */
const NAME_MAX_LENGTH = 60;

const HTTP_CONFLICT = 409;
const HTTP_UNPROCESSABLE_CONTENT = 422;

const UUID_PATTERN = /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/iu;

/**
 * The client-side half of the API's name rule: trimmed, then 1 to 60
 * characters, counted by code point as Go counts runes.
 *
 * @param raw - What the user typed.
 * @returns The name to send, or `undefined` when the API would refuse it.
 */
export function normalizeName(raw: string): string | undefined {
	const name = raw.trim();
	// Array.from, not a spread: oxlint's no-misused-spread flags spreading a
	// string directly, even though both iterate the same Unicode code points
	// — the same count Go's []rune conversion produces. Same fix as
	// `validateLinkPassword` in `link-password.ts`.
	// oxlint-disable-next-line unicorn/prefer-spread
	const length = Array.from(name).length;
	return length > 0 && length <= NAME_MAX_LENGTH ? name : undefined;
}

/** Every way a folder or tag write can fail, as the management page words it. */
export type NameFailure =
	| 'capReached'
	| 'nameInvalid'
	| 'nameTaken'
	| 'notFound'
	| 'rateLimited'
	| 'unauthenticated'
	| 'unknown';

/**
 * The folder and tag endpoints send 409 and 422 without a `location`, so this
 * reads the status the way `loadAuditLogPage` does, and only a create can hit
 * the cap.
 *
 * @param error - Whatever the failed folder or tag call threw.
 * @param atCap - Whether the team already had its cap of folders or tags; always false for a rename.
 * @returns The failure to show.
 */
export function nameFailureOf(error: unknown, atCap: boolean): NameFailure {
	const status = statusOf(error);
	if (status === HTTP_CONFLICT) return 'nameTaken';
	if (status === HTTP_UNPROCESSABLE_CONTENT) return atCap ? 'capReached' : 'nameInvalid';

	const { kind } = classifyApiError(error);
	if (kind === 'unauthenticated' || kind === 'notFound' || kind === 'rateLimited') return kind;
	return 'unknown';
}

/**
 * Parses a search parameter that may only name one entity by UUID, as the
 * folder and tag filters and `links/new`'s preselection do.
 *
 * @param value - The raw search parameter.
 * @returns The lowercased UUID, or `undefined`.
 */
export function parseUuidSearch(value: unknown): string | undefined {
	return typeof value === 'string' && UUID_PATTERN.test(value) ? value.toLowerCase() : undefined;
}
