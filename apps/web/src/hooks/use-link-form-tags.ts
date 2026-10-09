import { useQuery } from '@tanstack/react-query';

import type { TagCreateResult, TagOption } from '../components/tag-picker';
import { canEdit } from '../lib/team-roles';
import { tagsQueryOptions } from '../server/tags';
import { useCreateTag } from './use-create-tag';

// The loader's half of the tag wiring, exported with the hook so the edit page
// names both from here rather than from `server/tags`, one import fewer —
// `import/max-dependencies` caps that page at 20, and the hook already imports
// from `server/tags`.
export { prefetchTags } from '../server/tags';

/** The tag props both link pages hand `LinkForm`, prop for prop. */
export interface LinkFormTags {
	/** Editors and up, matching the API's EditorScope on tag creation. */
	readonly canCreateTags: boolean;
	readonly onCreateTag: (name: string) => Promise<TagCreateResult>;
	/** The team's tags, or an empty list while they are pending or failed. */
	readonly tags: readonly TagOption[];
	/** Whether `tags` is the team's real list, so a chosen tag missing from it can be called deleted. */
	readonly tagsLoaded: boolean;
}

/**
 * The tag field's wiring the create and edit link pages share: the team's
 * tags, whether they are real, who may create one, and the create call.
 * One hook rather than the same four lines in each page, which also keeps
 * the edit page within `import/max-dependencies`.
 *
 * Non-suspense, like the folders on both pages: the loader's `prefetchTags`
 * never rejects and has usually warmed this cache already, but a failed fetch
 * must leave a picker with no options rather than no page.
 *
 * @param teamId - The team whose tags to read.
 * @param role - The caller's role on the team, from `GET /v1/me`.
 * @returns The props `LinkForm` takes for its tag field.
 */
export function useLinkFormTags(teamId: string, role: string | undefined): LinkFormTags {
	const { data, isSuccess } = useQuery(tagsQueryOptions(teamId));
	const onCreateTag = useCreateTag(teamId);

	return { canCreateTags: canEdit(role), onCreateTag, tags: data ?? [], tagsLoaded: isSuccess };
}
