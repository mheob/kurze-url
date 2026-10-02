import { useMutation, useQueryClient } from '@tanstack/react-query';
import { useRouter } from '@tanstack/react-router';
import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import type { NamedItem, NameNamespace, NameRowError } from '../components/name-list';
import { nameFailureOf, type NameFailure } from '../lib/names';

/*
 * `useNameMutations` below is a thin composition of the helpers in this
 * block, not one long function: `max-statements` caps a `.ts` function at ten
 * (it is off for `.tsx`, which is why the route component this was lifted
 * out of never hit it), and each helper is one concern of the page —
 * refreshing after a write, wording a failure, the create form, the rows.
 * They sit above the exports because `import/exports-last` wants every
 * export contiguous at the end of the file.
 */

/**
 * The refetch after any successful write: this namespace's own list, and the
 * team's links, which embed folder and tag names.
 *
 * @param namespace - The query-key head of the list to refetch.
 * @param teamId - The team whose queries to refetch.
 * @returns The refetch.
 */
function useRefresh(namespace: NameNamespace, teamId: string): () => Promise<void> {
	const queryClient = useQueryClient();

	return async () => {
		await queryClient.invalidateQueries({ queryKey: [namespace, teamId] });
		await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	};
}

/**
 * Turns a failure into the words to show, or sends an expired session to
 * `/login` and shows nothing.
 *
 * @param namespace - Picks the copy for the failures that name the item (`folders.*` or `tags.*`).
 * @returns The mapping; `undefined` when the failure was handled by navigating away.
 */
function useFailureMessage(namespace: NameNamespace): (failure: NameFailure) => string | undefined {
	const { t } = useTranslation();
	const router = useRouter();

	return (failure) => {
		if (failure === 'unauthenticated') {
			void router.navigate({ to: '/login' });
			return undefined;
		}
		// The one wording both namespaces share: the name rule is the API's, not a
		// folder's or a tag's, so it lives under `names.*` beside `names.cancel`.
		if (failure === 'nameInvalid') return t('names.nameInvalid');
		if (failure === 'rateLimited' || failure === 'unknown') return t(`errors.${failure}`);
		return t(`${namespace}.${failure}`);
	};
}

/** What the two halves of the hook both need: how to refetch, and how to word a failure. */
interface Shared {
	readonly messageFor: (failure: NameFailure) => string | undefined;
	readonly refresh: () => Promise<void>;
}

/**
 * The create half: the mutation, the error it leaves on the name field and
 * the key that remounts the form after a success.
 *
 * @param options - The page's options; reads `cap`, `create` and `items`.
 * @param shared - How to refetch and how to word a failure.
 * @returns The props the create form needs.
 */
function useCreate(
	options: NameMutationsOptions,
	shared: Shared,
): Pick<NameMutations, 'createError' | 'createKey' | 'onCreate'> {
	const [createError, setCreateError] = useState<string | undefined>(undefined);
	const [createKey, setCreateKey] = useState(0);
	const create = useMutation({
		mutationFn: async (name: string) => options.create(name),
		onError: (error: unknown) => {
			setCreateError(shared.messageFor(nameFailureOf(error, options.items.length >= options.cap)));
		},
		onSuccess: async () => {
			setCreateError(undefined);
			setCreateKey((key) => key + 1); // remounts the form, clearing the field
			await shared.refresh();
		},
	});

	return {
		createError,
		createKey,
		onCreate: (name) => {
			create.mutate(name);
		},
	};
}

/**
 * The per-row half: rename and delete, the one error either may leave on a
 * row, and the heading that takes focus once a delete has removed the row
 * the reader was on.
 *
 * @param options - The page's options; reads `remove` and `rename`.
 * @param shared - How to refetch and how to word a failure.
 * @returns The props the list and the heading need.
 */
function useRows(
	options: NameMutationsOptions,
	shared: Shared,
): Omit<NameMutations, 'createError' | 'createKey' | 'onCreate'> {
	const [rowError, setRowError] = useState<NameRowError | null>(null);
	// Focused once a delete succeeds — the row it belonged to is now gone, so
	// nothing on the page is a better landing spot for a keyboard user than
	// the page's own heading (`NameManagementBody`'s `headingRef`).
	const headingRef = useRef<HTMLHeadingElement>(null);

	const remove = useMutation({
		mutationFn: async (itemId: string) => options.remove(itemId),
		onError: (error: unknown, itemId: string) => {
			const message = shared.messageFor(nameFailureOf(error, false));
			setRowError(message === undefined ? null : { action: 'delete', itemId, message });
		},
		onSuccess: async () => {
			setRowError(null);
			await shared.refresh();
			// The deleted row is gone from the DOM once `refresh` resolves and
			// this re-renders — nothing left in the list to return focus to, so
			// the heading is the next best landing spot, not `<body>`.
			headingRef.current?.focus();
		},
	});

	return {
		headingRef,
		onDelete: (itemId) => {
			remove.mutate(itemId);
		},
		// Passed to `NameList` as `onDismissError`: clears the row error, but
		// only when it still names this item — a dismiss firing after some
		// other row has already failed must not wipe out that newer error.
		onDismissError: (itemId) => {
			setRowError((current) => (current?.itemId === itemId ? null : current));
		},
		onRename: async (itemId, name) => {
			try {
				await options.rename(itemId, name);
				setRowError(null);
				await shared.refresh();
				return true;
			} catch (error) {
				const message = shared.messageFor(nameFailureOf(error, false));
				setRowError(message === undefined ? null : { action: 'rename', itemId, message });
				return false;
			}
		},
		rowError,
	};
}

/**
 * What the hook needs from the page that uses it. The three calls are the
 * page's own server functions, already bound to their input shape, so the
 * hook never learns whether it is managing folders or tags except through
 * `namespace`.
 */
export interface NameMutationsOptions {
	/** How many items a team may have; a 422 on create at this count means the cap, not a bad name. */
	readonly cap: number;
	readonly create: (name: string) => Promise<unknown>;
	readonly items: readonly NamedItem[];
	/** Picks the copy (`folders.*` or `tags.*`) and doubles as the query-key head `[namespace, teamId]` it invalidates. */
	readonly namespace: NameNamespace;
	readonly remove: (itemId: string) => Promise<unknown>;
	readonly rename: (itemId: string, name: string) => Promise<unknown>;
	readonly teamId: string;
}

/** What `NameManagementBody` takes from the hook, prop for prop. */
export interface NameMutations {
	readonly createError: string | undefined;
	readonly createKey: number;
	readonly headingRef: React.RefObject<HTMLHeadingElement | null>;
	readonly onCreate: (name: string) => void;
	readonly onDelete: (itemId: string) => void;
	readonly onDismissError: (itemId: string) => void;
	readonly onRename: (itemId: string, name: string) => Promise<boolean>;
	readonly rowError: NameRowError | null;
}

/**
 * The create, rename and delete wiring the folders and tags pages share: the
 * three mutations, the error each one leaves behind, the refetch after each
 * success and the focus handling after a delete. A page passes its own server
 * functions and gets back exactly the props `NameManagementBody` takes.
 *
 * @param options - The page's server functions, its items and the namespace it manages.
 * @returns The state and handlers for `NameManagementBody`.
 */
export function useNameMutations(options: NameMutationsOptions): NameMutations {
	const refresh = useRefresh(options.namespace, options.teamId);
	const messageFor = useFailureMessage(options.namespace);
	const shared = { messageFor, refresh };
	const creating = useCreate(options, shared);
	const rows = useRows(options, shared);

	return { ...creating, ...rows };
}
