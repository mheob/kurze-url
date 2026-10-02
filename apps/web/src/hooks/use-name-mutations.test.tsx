import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, renderHook, waitFor } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import type { NamedItem, NameNamespace } from '../components/name-list';
import { createI18n } from '../i18n';
import { useNameMutations, type NameMutations } from './use-name-mutations';

/**
 * The hook reaches the router only to send an expired session to `/login`,
 * so a one-method fake stands in for it — the same narrow-fake approach the
 * rest of this app's tests take instead of standing up a real router.
 */
const mocks = vi.hoisted(() => ({
	navigate: vi.fn<(options: Readonly<{ to: string }>) => Promise<void>>(),
}));

vi.mock('@tanstack/react-router', () => ({
	useRouter: () => ({ navigate: mocks.navigate }),
}));

interface MutationsOverrides {
	readonly cap?: number;
	readonly create?: (name: string) => Promise<unknown>;
	readonly items?: readonly NamedItem[];
	readonly namespace?: NameNamespace;
	readonly remove?: (itemId: string) => Promise<unknown>;
	readonly rename?: (itemId: string, name: string) => Promise<unknown>;
	readonly teamId?: string;
}

/**
 * A `create` that always fails the way the API does, with a bare status.
 *
 * @param status - The HTTP status the rejection carries.
 * @returns A create function that rejects with it.
 */
function creatingFails(status: number): (name: string) => Promise<unknown> {
	return vi.fn<(name: string) => Promise<unknown>>().mockRejectedValue({ status });
}

/**
 * Renders the hook inside the providers it reads from: a query client (so a
 * test can spy on the invalidations) and the i18n instance the copy comes
 * from. The defaults are the happy path: every call resolves, the team has no
 * items yet and the cap is the folders one.
 *
 * @param overrides - Partial options to override on the default fixture.
 * @returns The query client and the hook's live result.
 */
function renderMutations(overrides: MutationsOverrides = {}): {
	readonly queryClient: QueryClient;
	readonly result: { readonly current: NameMutations };
} {
	const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const i18n = createI18n('en');
	const { result } = renderHook(
		() =>
			useNameMutations({
				cap: overrides.cap ?? 100,
				create:
					overrides.create ?? vi.fn<(name: string) => Promise<unknown>>().mockResolvedValue({}),
				items: overrides.items ?? [],
				namespace: overrides.namespace ?? 'folders',
				remove:
					overrides.remove ?? vi.fn<(itemId: string) => Promise<unknown>>().mockResolvedValue({}),
				rename:
					overrides.rename ??
					vi.fn<(itemId: string, name: string) => Promise<unknown>>().mockResolvedValue({}),
				teamId: overrides.teamId ?? 'team-a',
			}),
		{
			// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactNode` is React's own type; not a declaration this file can edit.
			wrapper: ({ children }: { readonly children: React.ReactNode }) => (
				<QueryClientProvider client={queryClient}>
					<I18nextProvider i18n={i18n}>{children}</I18nextProvider>
				</QueryClientProvider>
			),
		},
	);
	return { queryClient, result };
}

describe(useNameMutations, () => {
	it('maps a 409 on create to the namespace copy and keeps the cap check per namespace', async () => {
		const { result } = renderMutations({
			cap: 200,
			create: creatingFails(409),
			items: [],
			namespace: 'tags',
		});
		act(() => {
			result.current.onCreate('Presse');
		});
		await waitFor(() => {
			expect(result.current.createError).toBe('A tag with this name already exists.');
		});
	});

	it('reports the cap only when the namespace cap is reached', async () => {
		const items = Array.from({ length: 100 }, (_, index) => ({
			id: `t${index}`,
			name: `T${index}`,
		}));
		const { result } = renderMutations({
			cap: 200,
			create: creatingFails(422),
			items,
			namespace: 'tags',
		});
		act(() => {
			result.current.onCreate('Neu');
		});
		await waitFor(() => {
			expect(result.current.createError).toBe('Enter a name of 1 to 60 characters.');
		});
	});

	it('reports the cap once the team has that many items', async () => {
		const items = Array.from({ length: 200 }, (_, index) => ({
			id: `t${index}`,
			name: `T${index}`,
		}));
		const { result } = renderMutations({
			cap: 200,
			create: creatingFails(422),
			items,
			namespace: 'tags',
		});
		act(() => {
			result.current.onCreate('Neu');
		});
		await waitFor(() => {
			expect(result.current.createError).toBe('A team can have at most 200 tags.');
		});
	});

	it('remounts the create form and clears its error after a successful create', async () => {
		const { result } = renderMutations({ namespace: 'folders' });
		expect(result.current.createKey).toBe(0);
		act(() => {
			result.current.onCreate('Sommerfest');
		});
		await waitFor(() => {
			expect(result.current.createKey).toBe(1);
		});
		expect(result.current.createError).toBeUndefined();
	});

	it('invalidates its own namespace and the team links after a rename', async () => {
		const { queryClient, result } = renderMutations({
			namespace: 'tags',
			rename: vi.fn<(itemId: string, name: string) => Promise<unknown>>().mockResolvedValue({}),
		});
		const spy = vi.spyOn(queryClient, 'invalidateQueries');
		await act(async () => {
			await result.current.onRename('t1', 'Neu');
		});
		expect(spy).toHaveBeenCalledWith({ queryKey: ['tags', 'team-a'] });
		expect(spy).toHaveBeenCalledWith({ queryKey: ['links', 'team-a'] });
	});

	it('keeps a failed rename as a row error tagged "rename" and resolves false', async () => {
		const { result } = renderMutations({
			namespace: 'folders',
			rename: vi
				.fn<(itemId: string, name: string) => Promise<unknown>>()
				.mockRejectedValue({ status: 409 }),
		});
		let saved: boolean | undefined = undefined;
		await act(async () => {
			saved = await result.current.onRename('f1', 'Sommerfest');
		});
		expect(saved).toBe(false);
		expect(result.current.rowError).toStrictEqual({
			action: 'rename',
			itemId: 'f1',
			message: 'A folder with this name already exists.',
		});

		act(() => {
			result.current.onDismissError('other');
		});
		expect(result.current.rowError).not.toBeNull();
		act(() => {
			result.current.onDismissError('f1');
		});
		expect(result.current.rowError).toBeNull();
	});

	it('keeps a failed delete as a row error tagged "delete"', async () => {
		const { result } = renderMutations({
			namespace: 'tags',
			remove: vi.fn<(itemId: string) => Promise<unknown>>().mockRejectedValue({ status: 404 }),
		});
		act(() => {
			result.current.onDelete('t1');
		});
		await waitFor(() => {
			expect(result.current.rowError).toStrictEqual({
				action: 'delete',
				itemId: 't1',
				message: 'This tag no longer exists.',
			});
		});
	});

	it('sends an expired session to /login without showing a message', async () => {
		const { result } = renderMutations({ create: creatingFails(401) });
		act(() => {
			result.current.onCreate('Presse');
		});
		await waitFor(() => {
			expect(mocks.navigate).toHaveBeenCalledWith({ to: '/login' });
		});
		expect(result.current.createError).toBeUndefined();
	});
});
