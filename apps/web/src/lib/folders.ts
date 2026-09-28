import { classifyApiError, statusOf, type ApiFailure } from './api-errors';

// FolderFilter/UNFILED_SEARCH_VALUE/FOLDERS_PER_TEAM stay here, ahead of the
// exports block below, even though that leaves these flagged by
// import(exports-last): parseFolderSearch/folderFilterOf/listFoldersFor (in
// server/folders.ts) read UNFILED_SEARCH_VALUE and FOLDERS_PER_TEAM by
// value, and eslint(no-use-before-define) checks a variable's textual
// position regardless of the enclosing function's hoisting, so moving these
// down would trade one lint rule's warning for the other — same tension
// `preferences.ts` and `auth.ts` document for their own top-of-file exports.
/* oxlint-disable import/exports-last -- see the comment above: moving these to the bottom of the
   file would only trade this warning for eslint(no-use-before-define) on the functions below that
   read UNFILED_SEARCH_VALUE/FOLDERS_PER_TEAM by value, which checks textual position, not hoisting. */
/** Which links the link list shows, by folder. */
export type FolderFilter =
	| { readonly kind: 'all' }
	| { readonly kind: 'unfiled' }
	| { readonly folderId: string; readonly kind: 'folder' };

/** The `folder` search parameter's value for "links without a folder". */
export const UNFILED_SEARCH_VALUE = 'none';

/** Mirrors maxFoldersPerTeam in apps/api/internal/api/limits.go. */
export const FOLDERS_PER_TEAM = 100;
/* oxlint-enable import/exports-last */

/** Mirrors the API's shared folder and tag name rule. */
const FOLDER_NAME_MAX_LENGTH = 60;

const HTTP_CONFLICT = 409;
const HTTP_UNPROCESSABLE_CONTENT = 422;

const UUID_PATTERN = /^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/iu;

/**
 * Parses a search parameter that may only name one folder, as `links/new`'s
 * preselection does.
 *
 * @param value - The raw search parameter.
 * @returns The lowercased UUID, or `undefined`.
 */
export function parseFolderIdSearch(value: unknown): string | undefined {
	return typeof value === 'string' && UUID_PATTERN.test(value) ? value.toLowerCase() : undefined;
}

/**
 * Parses the link list's `folder` search parameter. Anything but `none` or a
 * well-formed UUID is dropped, the way `page` drops a non-number.
 *
 * @param value - The raw search parameter.
 * @returns `none`, the lowercased UUID, or `undefined`.
 */
export function parseFolderSearch(value: unknown): string | undefined {
	if (value === UNFILED_SEARCH_VALUE) return value;
	return parseFolderIdSearch(value);
}

/**
 * @param folder - A value `parseFolderSearch` returned.
 * @returns The filter it names.
 */
export function folderFilterOf(folder: string | undefined): FolderFilter {
	if (folder === undefined) return { kind: 'all' };
	if (folder === UNFILED_SEARCH_VALUE) return { kind: 'unfiled' };
	return { folderId: folder, kind: 'folder' };
}

/**
 * @param filter - The filter to express.
 * @returns The `GET /v1/teams/{team_id}/links` query parameters for it.
 */
export function folderQueryOf(filter: FolderFilter): {
	readonly folder_id?: string;
	readonly unfiled?: boolean;
} {
	switch (filter.kind) {
		case 'all': {
			return {};
		}
		case 'unfiled': {
			return { unfiled: true };
		}
		case 'folder': {
			return { folder_id: filter.folderId };
		}
		default: {
			// Exhaustive over `FolderFilter` by construction: a case added to the
			// union without an arm above narrows `filter` to something other than
			// `never` here, and fails to compile — same pattern as
			// `failureMessageKey` in `member-invite-form.tsx`.
			const exhaustive: never = filter;
			return exhaustive;
		}
	}
}

/**
 * The client-side half of the API's name rule: trimmed, then 1 to 60
 * characters, counted by code point as Go counts runes.
 *
 * @param raw - What the user typed.
 * @returns The name to send, or `undefined` when the API would refuse it.
 */
export function normalizeFolderName(raw: string): string | undefined {
	const name = raw.trim();
	// Array.from, not a spread: oxlint's no-misused-spread flags spreading a
	// string directly, even though both iterate the same Unicode code points
	// — the same count Go's []rune conversion produces. Same fix as
	// `validateLinkPassword` in `link-password.ts`.
	// oxlint-disable-next-line unicorn/prefer-spread
	const length = Array.from(name).length;
	return length > 0 && length <= FOLDER_NAME_MAX_LENGTH ? name : undefined;
}

/** Every way a folder write can fail, as the folders page words it. */
export type FolderFailure =
	| 'capReached'
	| 'nameInvalid'
	| 'nameTaken'
	| 'notFound'
	| 'rateLimited'
	| 'unauthenticated'
	| 'unknown';

/**
 * The folder endpoints send 409 and 422 without a `location`, so this reads
 * the status the way `loadAuditLogPage` does, and only a create can hit the
 * cap.
 *
 * @param error - Whatever the failed folder call threw.
 * @param atCap - Whether the team already had FOLDERS_PER_TEAM folders; always false for a rename.
 * @returns The failure to show.
 */
export function folderFailureOf(error: unknown, atCap: boolean): FolderFailure {
	const status = statusOf(error);
	if (status === HTTP_CONFLICT) return 'nameTaken';
	if (status === HTTP_UNPROCESSABLE_CONTENT) return atCap ? 'capReached' : 'nameInvalid';

	const { kind } = classifyApiError(error);
	if (kind === 'unauthenticated' || kind === 'notFound' || kind === 'rateLimited') return kind;
	return 'unknown';
}

/**
 * The one `queryClient` method `remapFolderGoneFailure` needs — the same
 * narrowing every other injected `queryClient` dependency in this codebase
 * uses (e.g. `InvalidatableQueryClient` in `teams.$teamSlug.links.new.tsx`),
 * so a real `QueryClient` satisfies it structurally and a test can pass a
 * hand-built fake instead of standing up a real one.
 */
export interface FolderGoneQueryClient {
	readonly invalidateQueries: (
		filters: Readonly<{ queryKey: readonly unknown[] }>,
	) => Promise<void>;
}

/**
 * Grouped rather than three separate parameters, to keep
 * `remapFolderGoneFailure` under `max-params`' limit of two meaningful
 * arguments — the same reason `QrDownloadDeps`/`PasswordErrorHandlers` group
 * their own dependencies in `teams.$teamSlug.links.$linkId.tsx`.
 */
export interface FolderGoneRemapDeps {
	/** The translated message to show on the folder field — `t('links.folderGone')`. */
	readonly folderGoneMessage: string;
	/** Invalidated when `classified` names `folder_id`, so a retry does not keep offering the gone folder. */
	readonly queryClient: FolderGoneQueryClient;
	/** The team whose folders cache to refetch. */
	readonly teamId: string;
}

/**
 * Remaps a "folder deleted meanwhile" 422 (`body.folder_id`) into a message a
 * board member can act on, and refetches the team's stale folders cache
 * (the `['folders', teamId]` query key). Both the create and edit link
 * routes' own mutation `onError` handlers carried this exact check
 * (`teams.$teamSlug.links.new.tsx`, `teams.$teamSlug.links.$linkId.tsx`) —
 * one implementation is what keeps the two from drifting apart. Every other
 * failure passes through unchanged, and nothing is refetched for it.
 *
 * @param classified - The failure `classifyApiError` already produced.
 * @param deps - The message to show, and the query client/team to refetch through.
 * @returns `classified`, with `folder_id` reworded when it was present.
 */
export function remapFolderGoneFailure(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ApiFailure`'s `fields` variant nests a plain, mutable `Record<string, string>` (api-errors.ts); `Readonly<>` is shallow and cannot reach it, the same reason `teams.$teamSlug.links.$linkId.tsx`'s own file-level disable gives for this exact type.
	classified: ApiFailure,
	deps: FolderGoneRemapDeps,
): ApiFailure {
	if (classified.kind !== 'fields' || classified.fields.folder_id === undefined) return classified;

	void deps.queryClient.invalidateQueries({ queryKey: ['folders', deps.teamId] });
	return { ...classified, fields: { ...classified.fields, folder_id: deps.folderGoneMessage } };
}
