import type { Tag } from '@kurze-url/api-client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { renderHook, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type * as TagsModule from '../server/tags';
import { useLinkFormTags, type LinkFormTags } from './use-link-form-tags';

/** Same reason as `use-create-tag.test.tsx`: the list behind `tagsQueryOptions` is a server function, so only its fetcher is swapped. */
const mocks = vi.hoisted(() => ({ listTags: vi.fn<() => Promise<Tag[]>>() }));

vi.mock('../server/tags', async (importOriginal) => {
	const original = await importOriginal<typeof TagsModule>();
	return {
		...original,
		tagsQueryOptions: (teamId: string) => ({
			...original.tagsQueryOptions(teamId),
			queryFn: mocks.listTags,
		}),
	};
});

/**
 * Renders the hook for `team-a` inside the providers it reads from.
 *
 * @param role - The caller's role on the team.
 * @returns The query client and the hook's live result.
 */
function renderTags(role: string | undefined): {
	readonly queryClient: QueryClient;
	readonly result: { readonly current: LinkFormTags };
} {
	const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	const i18n = createI18n('en');
	const { result } = renderHook(() => useLinkFormTags('team-a', role), {
		// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactNode` is React's own type; not a declaration this file can edit.
		wrapper: ({ children }: { readonly children: React.ReactNode }) => (
			<QueryClientProvider client={queryClient}>
				<I18nextProvider i18n={i18n}>{children}</I18nextProvider>
			</QueryClientProvider>
		),
	});
	return { queryClient, result };
}

describe(useLinkFormTags, () => {
	it('hands over the team tags once they have loaded', async () => {
		mocks.listTags.mockResolvedValue([{ id: 't1', name: 'Jugend', team_id: 'team-a' }]);
		const { result } = renderTags('editor');
		await waitFor(() => {
			expect(result.current.tagsLoaded).toBe(true);
		});
		expect(result.current.tags).toStrictEqual([{ id: 't1', name: 'Jugend', team_id: 'team-a' }]);
	});

	it('reports tags that failed to load as not loaded, with no options', async () => {
		// What lets the form keep a link's chips without calling them deleted.
		mocks.listTags.mockRejectedValue(new Error('network'));
		const { queryClient, result } = renderTags('editor');
		await waitFor(() => {
			expect(queryClient.getQueryState(['tags', 'team-a'])?.status).toBe('error');
		});
		expect(result.current.tagsLoaded).toBe(false);
		expect(result.current.tags).toStrictEqual([]);
	});

	it('lets editors and up create tags, and nobody else', () => {
		mocks.listTags.mockResolvedValue([]);
		expect(renderTags('viewer').result.current.canCreateTags).toBe(false);
		expect(renderTags(undefined).result.current.canCreateTags).toBe(false);
		expect(renderTags('editor').result.current.canCreateTags).toBe(true);
		expect(renderTags('owner').result.current.canCreateTags).toBe(true);
	});
});
