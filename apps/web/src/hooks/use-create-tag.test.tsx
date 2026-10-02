import type { Tag } from '@kurze-url/api-client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import type { TagCreateResult } from '../components/tag-picker';
import { createI18n } from '../i18n';
import type * as TagsModule from '../server/tags';
import { useCreateTag } from './use-create-tag';

/**
 * `createTagFn` and the list behind `tagsQueryOptions` are server functions,
 * unreachable under Vitest ("No Start context found"). The create call is
 * replaced outright; `tagsQueryOptions` keeps its real key and only has its
 * fetcher swapped, so the cache this hook writes is the one the link form
 * actually reads.
 */
const mocks = vi.hoisted(() => ({
	createTagFn:
		vi.fn<
			(input: Readonly<{ data: Readonly<{ name: string; teamId: string }> }>) => Promise<Tag>
		>(),
	listTags: vi.fn<() => Promise<Tag[]>>(),
}));

vi.mock('../server/tags', async (importOriginal) => {
	const original = await importOriginal<typeof TagsModule>();
	return {
		...original,
		createTagFn: mocks.createTagFn,
		tagsQueryOptions: (teamId: string) => ({
			...original.tagsQueryOptions(teamId),
			queryFn: mocks.listTags,
		}),
	};
});

/**
 * Tags named `T0`, `T1` and so on, for seeding the cache.
 *
 * @param count - How many to make.
 * @returns The tags, all on `team-a`.
 */
function numberedTags(count: number): Tag[] {
	return Array.from({ length: count }, (_: unknown, index: number) => ({
		id: `t${index}`,
		name: `T${index}`,
		team_id: 'team-a',
	}));
}

/**
 * Renders the hook for `team-a` inside the providers it reads from.
 *
 * @param options - What the tags cache holds before the hook runs.
 * @param options.tagCount - Seeds the cache with that many tags; nothing is seeded when absent.
 * @param options.tags - Seeds the cache with exactly these tags; wins over `tagCount`.
 * @returns The query client and the hook's live result.
 */
function renderCreateTag(
	options: Readonly<{ tagCount?: number; tags?: readonly Readonly<Tag>[] }> = {},
): {
	readonly queryClient: QueryClient;
	readonly result: { readonly current: (name: string) => Promise<TagCreateResult> };
} {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const seed =
		options.tags ?? (options.tagCount === undefined ? undefined : numberedTags(options.tagCount));
	if (seed !== undefined) queryClient.setQueryData(['tags', 'team-a'], [...seed]);
	const i18n = createI18n('en');
	const { result } = renderHook(() => useCreateTag('team-a'), {
		// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactNode` is React's own type; not a declaration this file can edit.
		wrapper: ({ children }: { readonly children: React.ReactNode }) => (
			<QueryClientProvider client={queryClient}>
				<I18nextProvider i18n={i18n}>{children}</I18nextProvider>
			</QueryClientProvider>
		),
	});
	return { queryClient, result };
}

describe(useCreateTag, () => {
	it('returns the created tag and refreshes the tags', async () => {
		mocks.createTagFn.mockResolvedValue({ id: 't9', name: 'Vorstand', team_id: 'team-a' });
		const { queryClient, result } = renderCreateTag();
		const spy = vi.spyOn(queryClient, 'invalidateQueries');
		await expect(result.current('Vorstand')).resolves.toStrictEqual({
			tag: { id: 't9', name: 'Vorstand' },
		});
		expect(spy).toHaveBeenCalledWith({ queryKey: ['tags', 'team-a'] });
	});

	it('adds the created tag to the cached tags before resolving', async () => {
		// Until the refetch lands, the form would otherwise mark the new chip
		// "(deleted)" and the picker would offer to create the same name again.
		mocks.createTagFn.mockResolvedValue({ id: 't9', name: 'Vorstand', team_id: 'team-a' });
		const { queryClient, result } = renderCreateTag({ tagCount: 2 });
		await result.current('Vorstand');
		expect(queryClient.getQueryData(['tags', 'team-a'])).toStrictEqual([
			...numberedTags(2),
			{ id: 't9', name: 'Vorstand', team_id: 'team-a' },
		]);
	});

	it('does not add a created tag the cache already holds', async () => {
		const vorstand = { id: 't9', name: 'Vorstand', team_id: 'team-a' };
		mocks.createTagFn.mockResolvedValue(vorstand);
		const { queryClient, result } = renderCreateTag({ tags: [vorstand] });
		await result.current('Vorstand');
		expect(queryClient.getQueryData(['tags', 'team-a'])).toStrictEqual([vorstand]);
	});

	it('leaves tags that never loaded unloaded rather than seeding the new one alone', async () => {
		// A list of only the new tag would count as loaded, and the form would
		// then call every other chosen tag deleted.
		mocks.createTagFn.mockResolvedValue({ id: 't9', name: 'Vorstand', team_id: 'team-a' });
		const { queryClient, result } = renderCreateTag();
		await result.current('Vorstand');
		expect(queryClient.getQueryData(['tags', 'team-a'])).toBeUndefined();
	});

	it('on a 409 refetches and picks the existing tag of that name in any case', async () => {
		mocks.createTagFn.mockRejectedValue({ status: 409 });
		const { queryClient, result } = renderCreateTag();
		vi.spyOn(queryClient, 'query').mockResolvedValue([
			{ id: 't2', name: 'Presse', team_id: 'team-a' },
		]);
		await expect(result.current('presse')).resolves.toStrictEqual({
			tag: { id: 't2', name: 'Presse' },
		});
	});

	it('on a 409 refetches through the cache and leaves the existing tag in it', async () => {
		// The race: another member created "Presse" after this form loaded its
		// tags, so the cached list does not have it yet.
		const presse = { id: 't2', name: 'Presse', team_id: 'team-a' };
		mocks.createTagFn.mockRejectedValue({ status: 409 });
		mocks.listTags.mockResolvedValue([presse]);
		const { queryClient, result } = renderCreateTag({ tags: [] });
		await expect(result.current('presse')).resolves.toStrictEqual({
			tag: { id: 't2', name: 'Presse' },
		});
		expect(queryClient.getQueryData(['tags', 'team-a'])).toStrictEqual([presse]);
	});

	it('says the name is taken when the 409 refetch fails', async () => {
		mocks.createTagFn.mockRejectedValue({ status: 409 });
		const { queryClient, result } = renderCreateTag();
		vi.spyOn(queryClient, 'query').mockRejectedValue(new Error('network'));
		await expect(result.current('Presse')).resolves.toStrictEqual({
			error: 'A tag with this name already exists.',
		});
	});

	it('maps a 422 at the team cap to the cap message', async () => {
		mocks.createTagFn.mockRejectedValue({ status: 422 });
		const { result } = renderCreateTag({ tagCount: 200 });
		await expect(result.current('Neu')).resolves.toStrictEqual({
			error: 'A team can have at most 200 tags.',
		});
	});

	it('maps a 422 below the cap to the shared name rule', async () => {
		mocks.createTagFn.mockRejectedValue({ status: 422 });
		const { result } = renderCreateTag({ tagCount: 3 });
		await expect(result.current('Neu')).resolves.toStrictEqual({
			error: 'Enter a name of 1 to 60 characters.',
		});
	});

	it('maps an expired session to the generic failure', async () => {
		// The form has nowhere to send the visitor from a picker; the next save
		// meets the same 401 and goes to /login from there.
		mocks.createTagFn.mockRejectedValue({ status: 401 });
		const { result } = renderCreateTag();
		await expect(result.current('Neu')).resolves.toStrictEqual({
			error: 'Something went wrong. Please try again.',
		});
	});
});
