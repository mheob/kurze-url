import type { Tag } from '@kurze-url/api-client';
import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';

import type { TagCreateResult, TagOption } from '../components/tag-picker';
import { statusOf } from '../lib/api-errors';
import { nameFailureOf, type NameFailure } from '../lib/names';
import { TAGS_PER_TEAM } from '../lib/tags';
import { createTagFn, tagsQueryOptions } from '../server/tags';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is TanStack
   Query's own `QueryClient` class, a declaration this file cannot edit and `Readonly<>` cannot
   reach into. */

const HTTP_CONFLICT = 409;

/** What the helpers below need to reach the team's tags. */
interface TagsCache {
	readonly queryClient: QueryClient;
	readonly teamId: string;
}

/**
 * The i18n key for a failed create, under the same rule as the management
 * pages' own mapping: the name rule is shared, so it lives under `names.*`;
 * the failures that name a tag under `tags.*`; the rest under `errors.*`. An
 * expired session gets the generic message, because a picker has nowhere to
 * send the visitor from, and the form's next save meets the same 401 and
 * goes to `/login` from there.
 *
 * @param failure - The failure `nameFailureOf` produced.
 * @returns The key to translate.
 */
function failureKey(failure: NameFailure): string {
	if (failure === 'nameInvalid') return 'names.nameInvalid';
	if (failure === 'rateLimited' || failure === 'unknown') return `errors.${failure}`;
	if (failure === 'unauthenticated') return 'errors.unknown';
	return `tags.${failure}`;
}

/**
 * Adds a tag the API just created to the cached team tags, before the
 * refetch lands. Until it does, the form would mark the new chip "(deleted)"
 * and the picker would offer to create the same name again.
 *
 * A cache with no list yet is left alone. A list of only the new tag would
 * count as loaded, and the form would then mark every other chosen tag
 * deleted, which is exactly what it must not do for a link whose tags failed
 * to load.
 *
 * @param cache - The query client and team.
 * @param tag - The tag the API returned.
 */
function rememberTag(cache: TagsCache, tag: Tag): void {
	cache.queryClient.setQueryData(tagsQueryOptions(cache.teamId).queryKey, (tags) =>
		tags === undefined || tags.some((known: Tag) => known.id === tag.id) ? tags : [...tags, tag],
	);
}

/**
 * After a 409, finds the tag that already has `name`, in any case: names are
 * unique case-insensitively, so "presse" collides with "Presse". The refetch
 * goes through the cache and ignores its freshness, because the tag that
 * caused the conflict is usually one this form's list has not seen yet. The
 * fetch writes the cache, so the picker learns the tag the same way.
 *
 * @param cache - The query client and team.
 * @param name - The name the create was refused for.
 * @returns The existing tag, or `undefined` when the refetch failed or has none of that name.
 */
async function existingTag(cache: TagsCache, name: string): Promise<TagOption | undefined> {
	try {
		const tags = await cache.queryClient.query({
			...tagsQueryOptions(cache.teamId),
			staleTime: 0,
		});
		const lowered = name.toLowerCase();
		const match = tags.find((tag: Tag) => tag.name.toLowerCase() === lowered);
		return match === undefined ? undefined : { id: match.id, name: match.name };
	} catch {
		// The refetch failing leaves the conflict itself as the best thing to say.
		return undefined;
	}
}

/**
 * Creates a tag from the link form's picker. A 409 means the name exists
 * already (in some case — names are unique case-insensitively), so the tags
 * are refetched and that tag is returned instead of an error.
 *
 * @param teamId - The team the tag belongs to.
 * @returns A function that creates a tag by name.
 */
export function useCreateTag(teamId: string): (name: string) => Promise<TagCreateResult> {
	const { t } = useTranslation();
	const queryClient = useQueryClient();
	const cache = { queryClient, teamId };

	return async (name) => {
		try {
			const tag = await createTagFn({ data: { name, teamId } });
			rememberTag(cache, tag);
			void queryClient.invalidateQueries({ queryKey: ['tags', teamId] });
			return { tag: { id: tag.id, name: tag.name } };
		} catch (error) {
			const existing =
				statusOf(error) === HTTP_CONFLICT ? await existingTag(cache, name) : undefined;
			if (existing !== undefined) return { tag: existing };
			const tags = queryClient.getQueryData(tagsQueryOptions(teamId).queryKey) ?? [];
			return { error: t(failureKey(nameFailureOf(error, tags.length >= TAGS_PER_TEAM))) };
		}
	};
}
