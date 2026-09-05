import type { PageLink } from '@kurze-url/api-client';
import { Link } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { CopyButton } from './copy-button';
import { ShortUrlNotice } from './short-url-notice';

interface LinkListViewProps {
	readonly data: PageLink;
	readonly page: number;
	readonly teamId: string;
}

/**
 * Presentational: takes the already-fetched `PageLink` as a prop rather than
 * calling `useSuspenseQuery` itself, so it can be rendered — and its empty
 * state and `.invalid`-domain notice exercised — without a `QueryClient`,
 * a router loader, or a live API. `teams.$teamId.links.index.tsx` is the
 * only caller, wiring this to the query cache; `link-list.test.tsx` renders
 * it directly with hand-built `PageLink` fixtures instead.
 */
export function LinkList({ data, page, teamId }: LinkListViewProps): React.JSX.Element {
	const { t } = useTranslation();

	// `items` is nullable on the wire — `PageLink.items: Array<Link> | null`,
	// since Huma serialises a nil Go slice as JSON `null` — the same shape
	// `routes/_authed.tsx` already normalises for `memberships`.
	const items = data.items ?? [];
	const hasPreviousPage = data.page > 1;
	const hasNextPage = data.page * data.per_page < data.total_count;

	if (items.length === 0) {
		return (
			<p>
				{t('links.empty')}{' '}
				<Link params={{ teamId }} to="/teams/$teamId/links/new">
					{t('links.create')}
				</Link>
			</p>
		);
	}

	return (
		<>
			<h1>{t('links.heading')}</h1>
			{/* The entry point for a team that already has links — the empty
			    state above has its own, since it can never render both. */}
			<Link params={{ teamId }} to="/teams/$teamId/links/new">
				{t('links.create')}
			</Link>
			{/* Every link on this team was created against the same domain, so the
			    first item's hostname stands in for all of them. */}
			<ShortUrlNotice hostname={items[0]?.hostname ?? ''} />
			<ul>
				{items.map((link) => (
					<li key={link.id}>
						<a href={link.short_url}>{link.short_url}</a>
						<CopyButton value={link.short_url} />
						<span>{link.destination_url}</span>
					</li>
				))}
			</ul>
			<nav aria-label={t('links.paginationLabel')}>
				{hasPreviousPage ? (
					<Link params={{ teamId }} search={{ page: page - 1 }} to="/teams/$teamId/links">
						{t('links.previousPage')}
					</Link>
				) : (
					<span aria-disabled="true">{t('links.previousPage')}</span>
				)}
				{hasNextPage ? (
					<Link params={{ teamId }} search={{ page: page + 1 }} to="/teams/$teamId/links">
						{t('links.nextPage')}
					</Link>
				) : (
					<span aria-disabled="true">{t('links.nextPage')}</span>
				)}
			</nav>
		</>
	);
}
