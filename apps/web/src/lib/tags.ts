import type { ApiFailure } from './api-errors';
import type { FolderGoneQueryClient } from './folders';

/** Mirrors maxTagsPerTeam in apps/api/internal/api/limits.go. */
export const TAGS_PER_TEAM = 200;
/** Mirrors maxTagsPerLink in apps/api/internal/api/limits.go. */
export const TAGS_PER_LINK = 10;

/**
 * Whether two tag-id lists name the same set, ignoring order — what decides
 * whether an edit sends `tag_ids` at all.
 *
 * @param a - One list of tag ids.
 * @param b - The other.
 * @returns True when both name exactly the same tags.
 */
export function sameTagSet(a: readonly string[], b: readonly string[]): boolean {
	if (a.length !== b.length) return false;
	const set = new Set(a);
	return b.every((id) => set.has(id));
}

/** What `remapTagGoneFailure` needs from its caller. */
export interface TagGoneRemapDeps {
	/** Invalidated when `classified` names `tag_ids`, so a retry does not keep offering the gone tag. */
	readonly queryClient: FolderGoneQueryClient;
	/** The translated message to show on the tag field. */
	readonly tagGoneMessage: string;
	/** The team whose tags cache to refetch. */
	readonly teamId: string;
}

/**
 * The tag counterpart of `remapFolderGoneFailure`: a 422 on `body.tag_ids`
 * means a chosen tag was deleted meanwhile, so the field gets our own message
 * and the tags are refetched, which lets the picker mark the stale chip.
 *
 * @param classified - The failure `classifyApiError` produced.
 * @param deps - The message, query client and team.
 * @returns The failure, with the tag field's message replaced when it applies.
 */
export function remapTagGoneFailure(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ApiFailure`'s `fields` variant nests a plain, mutable `Record<string, string>` (api-errors.ts); `Readonly<>` is shallow and cannot reach it, the same reason `remapFolderGoneFailure` gives for this exact type.
	classified: ApiFailure,
	deps: TagGoneRemapDeps,
): ApiFailure {
	if (classified.kind !== 'fields' || classified.fields.tag_ids === undefined) return classified;

	void deps.queryClient.invalidateQueries({ queryKey: ['tags', deps.teamId] });
	return { ...classified, fields: { ...classified.fields, tag_ids: deps.tagGoneMessage } };
}
