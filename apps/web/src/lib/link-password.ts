/**
 * A browser-side copy of the link-password policy that
 * `apps/api/internal/auth/policy.go` enforces, so a rejection is immediate
 * rather than a round trip away.
 *
 * The API remains the enforcement point. This will drift from the Go rules,
 * and that is accepted: the worst outcome of drift is a message arriving a
 * round trip later than it could have, because a 422 still renders under the
 * field. Keep the reason tokens identical to the Go sentinels' `Error()`
 * strings — those are the wire contract, and `api-errors.ts` reads them back
 * out of the response.
 */

export type LinkPasswordReason =
	| 'derived_from_context'
	| 'too_common'
	| 'too_long'
	| 'too_repetitive'
	| 'too_short';

export interface LinkPasswordContext {
	destinationUrl: string;
	linkSlug: string;
	teamName: string;
	teamSlug: string;
}

export const MIN_LINK_PASSWORD_LENGTH = 8;
export const MAX_LINK_PASSWORD_LENGTH = 128;

const MIN_DISTINCT_CHARACTERS = 4;
const MIN_CONTEXT_TOKEN = 4;
const MIN_NORMALIZED_FOR_CONTEXT = 3;

/**
 * The same short list the Go policy embeds (`common-passwords.txt`), trimmed
 * to the entries a person types into a browser. Compared for equality
 * against the normalized password, never as a substring — substring matching
 * would reject `meinpasswortistlang` for containing `passwort`, and the false
 * rejections are harder to explain to a Verein than the passwords they would
 * catch.
 *
 * `passwort1` is deliberately absent: the design spec pins it as an accepted
 * password — the documented floor the length/repetition/context rules set —
 * and listing it here would reject it as `too_common` instead. `password1`
 * carries no such exception and stays.
 *
 * `ja` is the shortest entry, on purpose, mirroring the Go corpus: it is the
 * only realistic word whose normalized form is short enough to exercise the
 * "too short for the context comparison, still checked against this list"
 * path below.
 */
const COMMON_PASSWORDS = new Set([
	'passwort',
	'password',
	'password1',
	'12345678',
	'123456789',
	'qwertz',
	'qwerty',
	'geheim',
	'willkommen',
	'welcome',
	'verein',
	'vereinsheim',
	'mitglieder',
	'sommerfest',
	'vorstand',
	'letmein',
	'fussball',
	'admin123',
	'test1234',
	'ja',
]);

/**
 * German characters transliterated, lower case, everything outside [a-z0-9]
 * dropped — the same fold `normalizeForPolicy` performs in Go. The
 * transliteration is load-bearing: without it a team called `SV Grünwald`
 * does not catch `Gruenwald2026`, which is the password that team will pick.
 */
function normalize(value: string): string {
	return value
		.toLowerCase()
		.replaceAll('ä', 'ae')
		.replaceAll('ö', 'oe')
		.replaceAll('ü', 'ue')
		.replaceAll('ß', 'ss')
		.replaceAll(/[^a-z0-9]/gu, '');
}

function distinctCharacters(value: string): number {
	return new Set(value).size;
}

/**
 * The destination's hostname with a leading `www.` and its last label
 * removed, so `https://www.sv-gruenwald.de/verein` contributes
 * `sv-gruenwald` rather than `de`. An unparsable URL contributes nothing.
 */
function destinationLabel(destinationUrl: string): string {
	try {
		const labels = new URL(destinationUrl).hostname.replace(/^www\./u, '').split('.');
		return (labels.length > 1 ? labels.slice(0, -1) : labels).join('.');
	} catch {
		return '';
	}
}

function contextTokens(context: LinkPasswordContext): string[] {
	const sources = [context.linkSlug, context.teamName, context.teamSlug];
	const label = destinationLabel(context.destinationUrl);
	if (label !== '') sources.push(label);

	return sources
		.flatMap((source) => [source, ...source.split(/[-._\s]+/u)])
		.map(normalize)
		.filter((token) => token.length >= MIN_CONTEXT_TOKEN);
}

/**
 * Applies the policy in the fixed order `policy.go`'s `ValidatePassword`
 * does — length, repetition, context, then the common list — so the reason
 * returned for a password that trips several rules does not depend on
 * anything unwritten. Context runs before the common list on purpose: a word
 * that is both a context fixture and a common password (e.g. a Verein's own
 * event name) is reported as `derived_from_context`, the more specific and
 * more actionable reason, rather than the generic `too_common`.
 *
 * Returns the reason the password is refused, or `null` when it passes.
 */
export function validateLinkPassword(
	password: string,
	context: LinkPasswordContext,
): LinkPasswordReason | null {
	// Array.from, not a spread: oxlint's no-misused-spread flags spreading a
	// string directly, even though both iterate the same Unicode code points
	// — the same count Go's []rune conversion produces.
	const characters = Array.from(password);
	if (characters.length < MIN_LINK_PASSWORD_LENGTH) return 'too_short';
	if (characters.length > MAX_LINK_PASSWORD_LENGTH) return 'too_long';

	// Counted over the raw characters, not the normalized form: normalizing
	// first would collapse "!!!!a!!!!" to a single character and fail a
	// password that is merely odd.
	if (distinctCharacters(password) < MIN_DISTINCT_CHARACTERS) return 'too_repetitive';

	const normalized = normalize(password);

	// A normalized password too short to judge against context tokens is not
	// too short to look up in the common list: that lookup is equality, not
	// containment, so it isn't sensitive to length the way the containment
	// check is. Skip the context loop, not the whole function.
	if (normalized.length >= MIN_NORMALIZED_FOR_CONTEXT) {
		for (const token of contextTokens(context)) {
			// Both directions: "sommerfest" is contained by the slug
			// "sommerfest-2026", and "svgruenwaldsommerfest" contains the team
			// slug "sv-gruenwald".
			if (normalized.includes(token) || token.includes(normalized)) return 'derived_from_context';
		}
	}

	if (COMMON_PASSWORDS.has(normalized)) return 'too_common';
	return null;
}
