/**
 * Suggests a URL slug for a Verein's name. This is the only transliteration in
 * the running system: the Go API validates the format, rejects reserved values
 * and reports collisions, but never derives a slug — the maintainer submits the
 * value that ends up in every URL, and this only saves them the typing.
 *
 * German names are the normal input. `ä/ö/ü/ß` become `ae/oe/ue/ss` rather than
 * being dropped, because "grnwald" is not a name anybody recognises, and `&`
 * becomes `und` because that is how the name is read out loud. A trailing legal
 * form (`e.V.`, `e. V.`) carries no information in a URL and goes.
 *
 * The result can be empty — a name of nothing but punctuation has no slug — so
 * the form treats it as a suggestion, not a value it may submit unchecked.
 */
const TRANSLITERATIONS: readonly (readonly [RegExp, string])[] = [
	[/ä/g, 'ae'],
	[/ö/g, 'oe'],
	[/ü/g, 'ue'],
	[/ß/g, 'ss'],
	[/&/g, '-und-'],
];

export const TEAM_SLUG_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
export const TEAM_SLUG_MIN_LENGTH = 3;
export const TEAM_SLUG_MAX_LENGTH = 40;

export function suggestTeamSlug(name: string): string {
	let slug = name.toLowerCase().replace(/\s+e\.?\s*v\.?\s*$/, '');

	for (const [pattern, replacement] of TRANSLITERATIONS) {
		slug = slug.replace(pattern, replacement);
	}

	return slug
		.replace(/[^a-z0-9]+/g, '-')
		.replace(/^-+|-+$/g, '')
		.slice(0, TEAM_SLUG_MAX_LENGTH)
		.replace(/-+$/, '');
}
