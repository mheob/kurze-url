import type { AuditEntry } from '@kurze-url/api-client';
import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { resolveActor } from '../lib/audit-actor';
import { formatDateTime } from '../lib/format';
import type { Language } from '../lib/preferences';
import { Button } from './ui/button';
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from './ui/table';

/**
 * Every action the taxonomy defines (`apps/api/internal/audit/audit.go`),
 * mapped to its catalogue key as a `Record` rather than a lookup function —
 * the same shape `link-qr-card.tsx`'s `messageKeys` uses — so that adding an
 * action there without adding its label here is visible in one place, not
 * buried in a `switch`'s `default` branch. `action` arrives as a plain
 * `string` from the generated client even though the taxonomy is closed
 * server-side, which is exactly why `audit.actionUnknown` exists below.
 */
const actionLabelKeys: Record<string, string> = {
	'domain.claimed': 'audit.actionDomainClaimed',
	'domain.deleted': 'audit.actionDomainDeleted',
	'domain.verified': 'audit.actionDomainVerified',
	'folder.created': 'audit.actionFolderCreated',
	'folder.deleted': 'audit.actionFolderDeleted',
	'folder.updated': 'audit.actionFolderUpdated',
	'link.created': 'audit.actionLinkCreated',
	'link.deleted': 'audit.actionLinkDeleted',
	'link.password_changed': 'audit.actionPasswordChanged',
	'link.password_removed': 'audit.actionPasswordRemoved',
	'link.password_set': 'audit.actionPasswordSet',
	'link.updated': 'audit.actionLinkUpdated',
	'tag.created': 'audit.actionTagCreated',
	'tag.deleted': 'audit.actionTagDeleted',
	'tag.updated': 'audit.actionTagUpdated',
	'team.created': 'audit.actionTeamCreated',
	'team.renamed': 'audit.actionTeamRenamed',
	'team_member.added': 'audit.actionMemberAdded',
	'team_member.invited': 'audit.actionMemberInvited',
	'team_member.removed': 'audit.actionMemberRemoved',
	'team_member.role_changed': 'audit.actionMemberRoleChanged',
};

/**
 * Every `entity_type` the taxonomy defines, mapped to its catalogue key —
 * same reasoning as `actionLabelKeys` above for using a `Record`. Unlike
 * `action`, there is no `audit.entityUnknown` catalogue key: `EntryRow`
 * echoes an unrecognised value back raw instead, the same fallback
 * `domain-list.tsx`'s `statusLabel`/`reasonLabel` use for a
 * `verification_status` their own switch doesn't cover. That can't happen
 * for any of today's six values, but the same was true of `action` before
 * `password_set`/`password_changed`/`password_removed` were added — the
 * generated `entity_type: string` gives this rule no more guarantee of
 * completeness than `action`'s does, only fewer values today.
 */
const entityLabelKeys: Record<string, string> = {
	domain: 'audit.entityDomain',
	folder: 'audit.entityFolder',
	link: 'audit.entityLink',
	tag: 'audit.entityTag',
	team: 'audit.entityTeam',
	team_member: 'audit.entityTeamMember',
};

/**
 * `metadata` arrives as `unknown` from the generated client — Huma types it
 * as a free-form JSON object. Only a plain object is ever rendered; anything
 * else (an entry with no metadata at all, or a shape a future action might
 * carry that isn't an object) renders nothing rather than guessing.
 *
 * @param metadata - The entry's raw `metadata` field.
 * @returns Its own entries when it is a plain object, otherwise nothing.
 */
function metadataEntries(metadata: unknown): readonly (readonly [string, unknown])[] {
	if (typeof metadata !== 'object' || metadata === null) return [];
	return Object.entries(metadata);
}

/**
 * Recursive and generic on purpose — no per-action, per-shape rendering — so
 * that a twenty-second action never needs a twenty-second rendering rule
 * here. A plain object renders as its own `key: value` pairs, braced; an
 * array renders its items the same way this function would, bracketed. That
 * is load-bearing, not decorative: `link.updated`'s real metadata nests one
 * object per changed field — `apps/api/internal/api/links.go` writes
 * `metadata["slug"] = map[string]any{"from": ..., "to": ...}`, and the same
 * shape recurs for every other field it can change plus `tags`'s `{count}`
 * — so `String(value)` alone would print `[object Object]` for the single
 * most common action in the log. Handling the object case explicitly, before
 * ever falling through to `String`, is what makes that unreachable for any
 * input.
 *
 * Braces and brackets are also what keep an object distinguishable from an
 * array in the rendered text, and — for arrays specifically — what keeps a
 * value from colliding with an unrelated key: `metadata.changed` (see
 * `apps/api/internal/audit/audit.go`) is itself a list of the other keys in
 * the same object, so `{ changed: ['slug'], slug: 'sommerfest' }`'s bare-joined
 * array value would read as exactly `slug`, identical to the sibling key of
 * the same name. No structural fix avoids that: a `<dd>`, a nested `<li>` or
 * a `<span>` all still produce some element whose own text is exactly
 * `slug`, so `screen.getByText('slug')` finds two of them regardless of
 * markup. Only changing the text content — wrapping every array (and, for
 * the same reason, every object) in a fixed marker — resolves it.
 *
 * @param value - One metadata value, of whatever shape the action that wrote it chose.
 * @returns The value rendered as text, recursively: `{...}` for a plain object, `[...]` for an array, everything else through `String`.
 */
function metadataValueText(value: unknown): string {
	if (Array.isArray(value)) {
		return `[${value.map((item) => metadataValueText(item)).join(', ')}]`;
	}
	if (typeof value === 'object' && value !== null) {
		// Reuses `metadataEntries` rather than a second inline `Object.entries` —
		// one source of truth for "how to read an object's own entries," and it
		// already returns the readonly tuple shape this needs.
		const pairs = metadataEntries(value).map(
			([key, nested]) => `${key}: ${metadataValueText(nested)}`,
		);
		return `{${pairs.join(', ')}}`;
	}
	return String(value);
}

interface MetadataListProps {
	readonly metadata: unknown;
}

/**
 * The metadata pairs for one entry, as a `<dl>` of `<dt>`/`<dd>` pairs. Axe's
 * definition-list rule allows a `<dl>` to directly contain only `<div>`s that
 * themselves hold nothing but a `<dt>`/`<dd>` pair — the same constraint
 * `stat-summary.tsx` documents — so each pair gets its own wrapping `<div>`.
 *
 * Keys render raw, exactly as written in `metadata` (`changed`, `slug`, …):
 * they are protocol vocabulary, the same way `stat-breakdown-card.tsx` prints
 * a dimension's own values, not prose to run through a translation lookup.
 *
 * @param props - The component's props.
 * @param props.metadata - The entry's raw `metadata` field.
 * @returns The rendered definition list, or nothing when there is nothing to show.
 */
function MetadataList({ metadata }: MetadataListProps): React.JSX.Element | null {
	const entries = metadataEntries(metadata);
	if (entries.length === 0) return null;

	return (
		<dl>
			{entries.map(([key, value]) => (
				<div key={key}>
					<dt>{key}</dt>
					<dd>{metadataValueText(value)}</dd>
				</div>
			))}
		</dl>
	);
}

interface EntryRowProps {
	readonly entry: AuditEntry;
	readonly isOpen: boolean;
	readonly language: Language;
	readonly membersById: ReadonlyMap<string, string>;
	readonly onToggle: () => void;
}

/**
 * One audit entry, as its always-visible summary row plus a second `<tr>`
 * that renders only while its disclosure is open. The two rows share a
 * `<tbody>` with every other entry's pair — `AuditEntryTable` maps over
 * entries and renders one `EntryRow` each, not one `<table>` per entry — so
 * this component returns a fragment of two `<tr>`s rather than its own table.
 *
 * @param props - The component's props.
 * @param props.entry - The entry to render.
 * @param props.isOpen - Whether this entry's details row is currently shown.
 * @param props.language - The active language, for date formatting.
 * @param props.membersById - The team's current members, keyed by user id.
 * @param props.onToggle - Called when the disclosure button is activated.
 * @returns The entry's summary row and, while open, its details row.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- two causes in one destructure: `entry: AuditEntry` is generated codegen output whose properties aren't marked readonly, and `ReadonlyMap` (`membersById`) isn't recognised as readonly by this rule (same limitation `audit-actor.ts` documents).
function EntryRow({
	entry,
	isOpen,
	language,
	membersById,
	onToggle,
}: EntryRowProps): React.JSX.Element {
	const { t } = useTranslation();
	const detailsId = useId();

	const when = formatDateTime(entry.created_at, language);
	const actionLabelKey = actionLabelKeys[entry.action];
	const actionLabel = actionLabelKey === undefined ? t('audit.actionUnknown') : t(actionLabelKey);
	const entityLabelKey = entityLabelKeys[entry.entity_type];
	const entityLabel = entityLabelKey === undefined ? entry.entity_type : t(entityLabelKey);

	const actor = resolveActor(entry.actor_user_id, membersById);
	const actorLabel =
		actor.kind === 'member'
			? actor.email
			: t(actor.kind === 'formerMember' ? 'audit.actorFormerMember' : 'audit.actorDeletedAccount');

	// Some actions carry no metadata at all by design — `link.password_set`,
	// `_changed` and `_removed` are documented as empty on all three in
	// `apps/api/internal/audit/audit.go`, because the action name is already
	// the whole of what happened. Offering a disclosure control that opens
	// onto nothing is a dead affordance, worse than no control at all, so the
	// button — and the row it would open — only render when there is
	// something to show.
	const hasMetadata = metadataEntries(entry.metadata).length > 0;

	return (
		<>
			<TableRow>
				<TableCell>
					{when}
					{hasMetadata ? (
						// Plain "Details" would give every row's button the same
						// accessible name — `aria-label` overrides it with the action
						// and the timestamp interpolated in, while the visible text
						// stays the short, generic word every row shares. The visible
						// text is still a leading substring of the accessible name, so
						// this satisfies WCAG 2.5.3 (Label in Name).
						<Button
							aria-controls={detailsId}
							aria-expanded={isOpen}
							aria-label={t('audit.detailsFor', { action: actionLabel, when })}
							onClick={onToggle}
							type="button"
							variant="ghost"
						>
							{t('audit.details')}
						</Button>
					) : null}
				</TableCell>
				<TableCell>{actorLabel}</TableCell>
				<TableCell>{actionLabel}</TableCell>
				<TableCell>{entityLabel}</TableCell>
			</TableRow>
			{isOpen && hasMetadata ? (
				<TableRow id={detailsId}>
					<TableCell colSpan={4}>
						<MetadataList metadata={entry.metadata} />
					</TableCell>
				</TableRow>
			) : null}
		</>
	);
}

export interface AuditEntryTableProps {
	/** The page of entries to render, newest first — the API query's own `created_at desc, id desc`. Never re-sorted here. */
	readonly entries: readonly AuditEntry[];
	/** The active language, for date formatting. */
	readonly language: Language;
	/** The team's current members, keyed by user id — resolves an entry's actor through `resolveActor`. */
	readonly membersById: ReadonlyMap<string, string>;
}

/**
 * The audit log's entry table: four columns — when, who, what, which entity —
 * one row per entry, newest first, each row able to disclose its own
 * metadata. Presentational, like `LinkList`: it takes already-fetched entries
 * and the team's member map as props, rather than fetching either itself.
 *
 * Which entries are open is one `Set<number>` of entry ids held here, keyed
 * by `entry.id` rather than array index, so a row's open/closed state cannot
 * follow the wrong entry across a re-render that reorders the page.
 *
 * @param props - The component's props.
 * @param props.entries - The page of entries to render, newest first.
 * @param props.language - The active language, for date formatting.
 * @param props.membersById - The team's current members, keyed by user id.
 * @returns The rendered entry table.
 */
// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same two causes as `EntryRow` above: `AuditEntry` (`entries`) is generated codegen output, and `ReadonlyMap` (`membersById`) isn't recognised as readonly by this rule.
export function AuditEntryTable({
	entries,
	language,
	membersById,
}: AuditEntryTableProps): React.JSX.Element {
	const { t } = useTranslation();
	const [openEntryIds, setOpenEntryIds] = useState<ReadonlySet<number>>(new Set());

	function toggle(entryId: number): void {
		// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ReadonlySet` is TypeScript's immutable set type; the rule does not recognise it as readonly, the same limitation `ReadonlyMap` has (see `audit-actor.ts`).
		setOpenEntryIds((current) => {
			const next = new Set(current);
			if (next.has(entryId)) {
				next.delete(entryId);
			} else {
				next.add(entryId);
			}
			return next;
		});
	}

	return (
		<Table>
			<TableHeader>
				<TableRow>
					<TableHead>{t('audit.columnWhen')}</TableHead>
					<TableHead>{t('audit.columnWho')}</TableHead>
					<TableHead>{t('audit.columnWhat')}</TableHead>
					<TableHead>{t('audit.columnEntity')}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `entry: AuditEntry` is generated codegen output whose properties aren't marked readonly. */}
				{entries.map((entry) => (
					<EntryRow
						entry={entry}
						isOpen={openEntryIds.has(entry.id)}
						key={entry.id}
						language={language}
						membersById={membersById}
						onToggle={() => {
							toggle(entry.id);
						}}
					/>
				))}
			</TableBody>
		</Table>
	);
}
