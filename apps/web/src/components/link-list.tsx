/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file traces to `@kurze-url/api-client`'s generated `PageLink`/`Link` types, whose properties
   (and `Link.tags`'s nested array) are not marked readonly; that is generated codegen output,
   never edited by hand. */

import type { Folder, PageLink } from '@kurze-url/api-client';
import { Link } from '@tanstack/react-router';
import { LockIcon } from 'lucide-react';
import { useId } from 'react';
import { useTranslation } from 'react-i18next';

import { UNFILED_SEARCH_VALUE } from '../lib/folders';
import { CopyButton } from './copy-button';
import { ShortUrlNotice } from './short-url-notice';
import { Badge } from './ui/badge';
import { Empty, EmptyDescription } from './ui/empty';
import { Field, FieldLabel } from './ui/field';
import { NativeSelect, NativeSelectOption } from './ui/native-select';
import { Pagination, PaginationContent, PaginationItem } from './ui/pagination';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';

interface LinkListViewProps {
	readonly data: PageLink;
	/** The active `folder` search value: absent for "all folders", `UNFILED_SEARCH_VALUE` for "no folder", otherwise a folder id. */
	readonly folder: string | undefined;
	/**
	 * The team's folders, for the filter's options and the column's names.
	 * `undefined` means the folders query hasn't resolved yet — while loading
	 * or on a failed fetch — as opposed to `[]`, which means it resolved and
	 * the team has none. The distinction matters for exactly one thing: an
	 * unrecognised `folder` id is only ever reported as `links.folderMissing`
	 * once the folders are actually known, since before that "unknown folder"
	 * and "haven't loaded yet" are indistinguishable and the message would
	 * flash for a perfectly valid folder on every page load.
	 */
	readonly folders: readonly Folder[] | undefined;
	/** Reports the reader's newly chosen filter value; navigation is the caller's job, the same split `AuditFilterBarProps.onChange` uses. */
	readonly onFolderChange: (folder: string | undefined) => void;
	readonly page: number;
	readonly teamSlug: string;
}

// An if-chain, not a ternary: oxlint's `no-nested-ternary` is error-level and
// three catalogue keys chosen from two independent checks (`folder ===
// undefined`, `folder === UNFILED_SEARCH_VALUE`) nest no matter which two are
// compared first. Returns the key rather than the translated text so it can
// sit outside the component, next to nothing that needs `t`.
function emptyMessageKey(
	folder: string | undefined,
): 'links.empty' | 'links.emptyInFolder' | 'links.emptyUnfiled' {
	if (folder === undefined) return 'links.empty';
	if (folder === UNFILED_SEARCH_VALUE) return 'links.emptyUnfiled';
	return 'links.emptyInFolder';
}

/**
 * Presentational: takes the already-fetched `PageLink` as a prop rather than
 * calling `useSuspenseQuery` itself, so it can be rendered — and its empty
 * state and `.invalid`-domain notice exercised — without a `QueryClient`,
 * a router loader, or a live API. `teams.$teamSlug.links.index.tsx` is the
 * only caller, wiring this to the query cache; `link-list.test.tsx` renders
 * it directly with hand-built `PageLink` fixtures instead. This component
 * only ever used the team value for navigation, so it takes the slug rather
 * than the id — unlike its caller, it never feeds an API call.
 *
 * @param props - The component's props.
 * @param props.data - The already-fetched page of links.
 * @param props.folder - The active `folder` search value.
 * @param props.folders - The team's folders, or `undefined` while they have not loaded yet.
 * @param props.onFolderChange - Reports the reader's newly chosen filter value.
 * @param props.page - The current page number.
 * @param props.teamSlug - The team's slug, used only for navigation links.
 * @returns The rendered link list, or an empty-state message.
 */
export function LinkList({
	data,
	folder,
	folders,
	onFolderChange,
	page,
	teamSlug,
}: LinkListViewProps): React.JSX.Element {
	const { t } = useTranslation();
	const filterId = useId();

	// `items` is nullable on the wire — `PageLink.items: Array<Link> | null`,
	// since Huma serialises a nil Go slice as JSON `null` — the same shape
	// `routes/_authed.tsx` already normalises for `memberships`.
	const items = data.items ?? [];
	const hasPreviousPage = data.page > 1;
	const hasNextPage = data.page * data.per_page < data.total_count;

	// Every link used to share one domain, so the first item's hostname could
	// stand in for all of them — the domain picker ended that: a team can mix
	// links on the shared instance hostname with links on a custom domain it
	// verified. `ShortUrlNotice` only ever renders for a hostname ending in
	// `.invalid` (Preview's placeholder `SHARED_DOMAIN_HOSTNAME`), so finding
	// any one link still on it is enough to justify showing the warning —
	// there is no need to name every link it applies to, only to not miss it
	// because a later, unrelated link happened to render first.
	const invalidHostname = items.find((item) => item.hostname.endsWith('.invalid'))?.hostname ?? '';

	const knownFolders = folders ?? [];
	const selected = knownFolders.find((candidate) => candidate.id === folder);
	// `folders !== undefined` is what makes this wait for the folders to have
	// actually loaded — see the prop's own docstring.
	const missing =
		folders !== undefined &&
		folder !== undefined &&
		folder !== UNFILED_SEARCH_VALUE &&
		selected === undefined;
	const folderNames = new Map(knownFolders.map((candidate) => [candidate.id, candidate.name]));
	const newLinkSearch = selected === undefined ? {} : { folder: selected.id };
	const emptyText = t(emptyMessageKey(folder));

	// Split out of the return statement's own JSX so the `missing` check below
	// is a single ternary, not one nested inside another — oxlint's
	// `no-nested-ternary` (both the eslint and unicorn copies of the rule) is
	// error-level, and this reads the same either way.
	const listOrEmptyState =
		items.length === 0 ? (
			<Empty>
				<EmptyDescription>{emptyText}</EmptyDescription>
			</Empty>
		) : (
			<>
				<ShortUrlNotice hostname={invalidHostname} />
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>{t('links.columnShortUrl')}</TableHead>
							<TableHead>{t('links.columnDestination')}</TableHead>
							<TableHead>{t('links.columnFolder')}</TableHead>
							<TableHead>{t('links.columnActions')}</TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{items.map((link) => (
							<TableRow key={link.id}>
								<TableCell>
									<a href={link.short_url}>{link.short_url}</a>
									<CopyButton value={link.short_url} />
									{link.has_password ? (
										<Badge variant="secondary">
											<LockIcon aria-hidden />
											{t('links.passwordBadge')}
										</Badge>
									) : null}
								</TableCell>
								<TableCell>{link.destination_url}</TableCell>
								<TableCell>
									{link.folder_id === undefined ? (
										<>
											{/* The dash itself goes through `t()`, not a literal: `react/jsx-no-literals`
											    is error-level project-wide, and a typographic mark still counts —
											    `catalogues.test.ts`'s `identicalByDesign` list is where it's excused
											    from also needing a *different* German value. */}
											<span aria-hidden>{t('links.folderNoneMark')}</span>
											<span className="sr-only">{t('links.folderNone')}</span>
										</>
									) : (
										<Link
											params={{ teamSlug }}
											search={{ folder: link.folder_id, page: 1 }}
											to="/teams/$teamSlug/links"
										>
											{folderNames.get(link.folder_id) ?? link.folder_id}
										</Link>
									)}
								</TableCell>
								<TableCell>
									<Link params={{ linkId: link.id, teamSlug }} to="/teams/$teamSlug/links/$linkId">
										{t('links.edit')}
									</Link>
								</TableCell>
							</TableRow>
						))}
					</TableBody>
				</Table>
				<Pagination aria-label={t('links.paginationLabel')}>
					<PaginationContent>
						<PaginationItem>
							{hasPreviousPage ? (
								<Link
									params={{ teamSlug }}
									search={{ folder, page: page - 1 }}
									to="/teams/$teamSlug/links"
								>
									{t('links.previousPage')}
								</Link>
							) : (
								<span aria-disabled="true">{t('links.previousPage')}</span>
							)}
						</PaginationItem>
						<PaginationItem>
							{hasNextPage ? (
								<Link
									params={{ teamSlug }}
									search={{ folder, page: page + 1 }}
									to="/teams/$teamSlug/links"
								>
									{t('links.nextPage')}
								</Link>
							) : (
								<span aria-disabled="true">{t('links.nextPage')}</span>
							)}
						</PaginationItem>
					</PaginationContent>
				</Pagination>
			</>
		);

	return (
		<>
			<h1>{t('links.heading')}</h1>
			{selected === undefined ? null : <p>{t('links.inFolder', { name: selected.name })}</p>}
			{folder === UNFILED_SEARCH_VALUE ? <p>{t('links.folderNone')}</p> : null}
			{/* Always here, filtered or not, populated or empty — the empty
			    state used to carry its own "create" link, which this
			    subsumes; see link-list.test.tsx for the single-link
			    assertion that pins there being only one. */}
			<Link params={{ teamSlug }} search={newLinkSearch} to="/teams/$teamSlug/links/new">
				{t('links.create')}
			</Link>
			<Field>
				<FieldLabel htmlFor={filterId}>{t('links.folderFilter')}</FieldLabel>
				<NativeSelect
					id={filterId}
					onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
						const { value } = event.target;
						onFolderChange(value === '' ? undefined : value);
					}}
					value={folder ?? ''}
				>
					<NativeSelectOption value="">{t('links.folderAll')}</NativeSelectOption>
					<NativeSelectOption value={UNFILED_SEARCH_VALUE}>
						{t('links.folderNone')}
					</NativeSelectOption>
					{knownFolders.map((candidate) => (
						<NativeSelectOption key={candidate.id} value={candidate.id}>
							{candidate.name}
						</NativeSelectOption>
					))}
				</NativeSelect>
			</Field>
			{missing ? (
				<p>
					{t('links.folderMissing')}{' '}
					<Link params={{ teamSlug }} search={{ page: 1 }} to="/teams/$teamSlug/links">
						{t('links.showAllLinks')}
					</Link>
				</p>
			) : (
				listOrEmptyState
			)}
		</>
	);
}
