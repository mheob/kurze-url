import type { Folder, PageFolder } from '@kurze-url/api-client';
import { useMutation, useQueryClient, useSuspenseQuery } from '@tanstack/react-query';
import { createFileRoute, Navigate, redirect, useRouter } from '@tanstack/react-router';
import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { FolderForm } from '../../components/folder-form';
import { FolderList, type FolderRowError } from '../../components/folder-list';
import { classifyApiError, type ApiFailure } from '../../lib/api-errors';
import { FOLDERS_PER_TEAM, folderFailureOf, type FolderFailure } from '../../lib/folders';
import { reportUnexpected } from '../../lib/observability';
import { canEdit } from '../../lib/team-roles';
import {
	createFolderFn,
	deleteFolderFn,
	foldersQueryOptions,
	renameFolderFn,
} from '../../server/folders';
import { requireTeamId } from '../_authed';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding below is typed by
   TanStack Router/Query's own option shapes — `beforeLoad`/`loader`'s parameter, and
   `queryOptions()`'s own return type via `ReturnType<typeof foldersQueryOptions>` — none of which
   is a declaration this file can edit. Same deviation `teams.$teamSlug.domains.tsx` documents. */

/**
 * The one method this loader reaches through on `context.queryClient` — same
 * reasoning as `DomainsDataSource`/`LinksDataSource`: a real `QueryClient`
 * satisfies this structurally, so the loader needs no cast.
 */
interface FoldersDataSource {
	readonly ensureQueryData: (
		options: ReturnType<typeof foldersQueryOptions>,
	) => Promise<PageFolder>;
}

/**
 * Same shape and reasoning as `loadDomains`/`loadLinks`: a 401 that survives
 * to this loader must not fall through to `errorComponent` as dead-end
 * inline text — it sends the visitor back to `/login` instead. Every other
 * error kind is rethrown unchanged.
 *
 * @param queryClient - The query client to fetch through; only needs `ensureQueryData`.
 * @param teamId - The team's id, already resolved from its slug.
 * @returns The team's folders.
 */
export async function loadFolders(
	queryClient: FoldersDataSource,
	teamId: string,
): Promise<PageFolder> {
	try {
		return await queryClient.ensureQueryData(foldersQueryOptions(teamId));
	} catch (error) {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		if (classifyApiError(error).kind === 'unauthenticated') throw redirect({ to: '/login' });
		throw error;
	}
}

export const Route = createFileRoute('/_authed/teams/$teamSlug/folders')({
	beforeLoad: ({ context, params }) => ({
		role: context.me.memberships.find((membership) => membership.slug === params.teamSlug)?.role,
		teamId: requireTeamId(context.me.memberships, params.teamSlug),
	}),
	component: RouteComponent,
	errorComponent: FoldersError,
	loader: async ({ context }) => loadFolders(context.queryClient, context.teamId),
});

/**
 * Same reasoning as `LinksError`/`DomainsError`: a list that silently
 * rendered empty on a failed request would be indistinguishable from a team
 * with no folders, so this fails loudly instead. `kind: 'unauthenticated'`
 * can still reach here on a background refetch, a path `loadFolders`'s own
 * try/catch never sees — hence the `<Navigate>`.
 *
 * @param props - The route's error-boundary props.
 * @param props.error - Whatever the loader or query threw.
 * @returns A redirect to `/login` for an expired session, otherwise the failure rendered inline.
 */
export function FoldersError({ error }: { readonly error: unknown }): React.JSX.Element {
	const { t } = useTranslation();
	const failure: ApiFailure = classifyApiError(error);

	reportUnexpected(error);

	if (failure.kind === 'unauthenticated') return <Navigate to="/login" />;

	const key = failure.kind === 'fields' ? 'unknown' : failure.kind;

	return <p role="alert">{t(`errors.${key}`)}</p>;
}

export interface FoldersPageBodyProps {
	readonly canEdit: boolean;
	/** Shown on the create form's name field; `undefined` once creation succeeds or nothing has failed yet. */
	readonly createError: string | undefined;
	/** Remounts the create form on every successful create, clearing its field — see `RouteComponent`'s own comment on `setCreateKey`. */
	readonly createKey: number;
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `Folder` is the generated `@kurze-url/api-client` type; see the same disable on `FolderList`'s own `folders` prop.
	readonly folders: readonly Folder[];
	/** Focused once the page heading regains focus after a delete, so a keyboard user lands somewhere meaningful rather than at `<body>`. */
	readonly headingRef: React.RefObject<HTMLHeadingElement | null>;
	readonly onCreate: (name: string) => void;
	readonly onDelete: (folderId: string) => void;
	readonly onDismissError: (folderId: string) => void;
	readonly onRename: (folderId: string, name: string) => Promise<boolean>;
	readonly rowError: FolderRowError | null;
	readonly teamSlug: string;
}

/**
 * The presentational body of the folders page — pure and prop-driven, the
 * same idiom `MembersPageBody`/`AuditLogPageBody` already use so the route's
 * router wiring (`RouteComponent` below) can be tested separately from what
 * it renders (folders-frontend final review, Minor 3).
 *
 * @param props - The component's props.
 * @param props.canEdit - Whether the caller may create, rename and delete.
 * @param props.createError - Shown on the create form's name field.
 * @param props.createKey - Remounts the create form on every successful create.
 * @param props.folders - The team's folders, already fetched by the caller.
 * @param props.headingRef - Focused after a successful delete.
 * @param props.onCreate - Creates a folder with the given name.
 * @param props.onDelete - Deletes the folder with the given id.
 * @param props.onDismissError - Clears a row's error; called when its rename form is cancelled and when it is opened again.
 * @param props.onRename - Renames a folder; resolves true on success, which closes the inline form.
 * @param props.rowError - The last failure, the row it happened on, and which action produced it.
 * @param props.teamSlug - The team's slug, for the links into the filtered list.
 * @returns The rendered page body.
 */
export function FoldersPageBody({
	canEdit: editor,
	createError,
	createKey,
	folders,
	headingRef,
	onCreate,
	onDelete,
	onDismissError,
	onRename,
	rowError,
	teamSlug,
}: FoldersPageBodyProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<>
			{/* tabIndex so a successful delete can move focus here — see
			    `RouteComponent`'s `remove` mutation — even though a plain
			    heading is not normally in the tab order. */}
			<h1 ref={headingRef} tabIndex={-1}>
				{t('folders.heading')}
			</h1>
			<p>{t('folders.intro')}</p>
			{editor ? (
				<FolderForm
					// Only after the first create, never on the form's own initial
					// mount (a page load) — the same distinction `FolderRow`'s
					// `autoFocus` on its rename form draws, and why that one earns
					// the identical disable below: the rule can't tell a remount
					// triggered by the reader's own submit apart from the page-load
					// antipattern it actually guards against.
					// oxlint-disable-next-line jsx-a11y/no-autofocus
					autoFocus={createKey > 0}
					error={createError}
					key={createKey}
					label={t('folders.name')}
					onSubmit={onCreate}
					submitLabel={t('folders.create')}
				/>
			) : null}
			<FolderList
				canEdit={editor}
				folders={folders}
				onDelete={onDelete}
				onDismissError={onDismissError}
				onRename={onRename}
				rowError={rowError}
				teamSlug={teamSlug}
			/>
		</>
	);
}

function RouteComponent(): React.JSX.Element {
	const { teamSlug } = Route.useParams();
	const { role, teamId } = Route.useRouteContext();
	const { t } = useTranslation();
	const router = useRouter();
	const queryClient = useQueryClient();
	const { data } = useSuspenseQuery(foldersQueryOptions(teamId));
	const folders = data.items ?? [];
	const editor = canEdit(role);
	const [createError, setCreateError] = useState<string | undefined>(undefined);
	const [createKey, setCreateKey] = useState(0);
	const [rowError, setRowError] = useState<FolderRowError | null>(null);
	// Focused once a delete succeeds — the row it belonged to is now gone, so
	// nothing on the page is a better landing spot for a keyboard user than
	// the page's own heading (`FoldersPageBody`'s `headingRef`).
	const heading = useRef<HTMLHeadingElement>(null);

	const refresh = async (): Promise<void> => {
		await queryClient.invalidateQueries({ queryKey: ['folders', teamId] });
		await queryClient.invalidateQueries({ queryKey: ['links', teamId] });
	};

	const messageFor = (failure: FolderFailure): string | undefined => {
		if (failure === 'unauthenticated') {
			void router.navigate({ to: '/login' });
			return undefined;
		}
		if (failure === 'notFound') return t('folders.notFound');
		if (failure === 'rateLimited' || failure === 'unknown') return t(`errors.${failure}`);
		return t(`folders.${failure}`);
	};

	// Passed to `FolderList` as `onDismissError`: clears the row error, but
	// only when it still names this folder — a dismiss firing after some
	// other row has already failed must not wipe out that newer error.
	const dismissRowError = (folderId: string): void => {
		setRowError((current) => (current?.folderId === folderId ? null : current));
	};

	const create = useMutation({
		mutationFn: async (name: string) => createFolderFn({ data: { name, teamId } }),
		onError: (error: unknown) => {
			setCreateError(messageFor(folderFailureOf(error, folders.length >= FOLDERS_PER_TEAM)));
		},
		onSuccess: async () => {
			setCreateError(undefined);
			setCreateKey((key) => key + 1); // remounts the form, clearing the field
			await refresh();
		},
	});

	const onRename = async (folderId: string, name: string): Promise<boolean> => {
		try {
			await renameFolderFn({ data: { folderId, name } });
			setRowError(null);
			await refresh();
			return true;
		} catch (error) {
			const message = messageFor(folderFailureOf(error, false));
			setRowError(message === undefined ? null : { action: 'rename', folderId, message });
			return false;
		}
	};

	const remove = useMutation({
		mutationFn: async (folderId: string) => deleteFolderFn({ data: { folderId } }),
		onError: (error: unknown, folderId: string) => {
			const message = messageFor(folderFailureOf(error, false));
			setRowError(message === undefined ? null : { action: 'delete', folderId, message });
		},
		onSuccess: async () => {
			setRowError(null);
			await refresh();
			// The deleted row is gone from the DOM once `refresh` resolves and
			// this re-renders — nothing left in the list to return focus to, so
			// the heading is the next best landing spot, not `<body>`.
			heading.current?.focus();
		},
	});

	return (
		<FoldersPageBody
			canEdit={editor}
			createError={createError}
			createKey={createKey}
			folders={folders}
			headingRef={heading}
			onCreate={(name) => {
				create.mutate(name);
			}}
			onDelete={(folderId) => {
				remove.mutate(folderId);
			}}
			onDismissError={dismissRowError}
			onRename={onRename}
			rowError={rowError}
			teamSlug={teamSlug}
		/>
	);
}
