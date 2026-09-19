import { useId } from 'react';
import { useTranslation } from 'react-i18next';

import {
	AUDIT_ENTITY_TYPES,
	hasActiveFilters,
	type AuditEntityType,
	type AuditFilters,
} from '../lib/audit-filters';
import { Button } from './ui/button';
import { Field, FieldGroup, FieldLabel } from './ui/field';
import { Input } from './ui/input';
import { NativeSelect, NativeSelectOption } from './ui/native-select';

/**
 * Every `AuditEntityType` mapped to its catalogue key. A `Record` keyed by
 * the union itself, not `string` the way `audit-entry-table.tsx`'s own
 * `entityLabelKeys` has to be — that file renders a generated `entity_type:
 * string` it cannot fully trust, where this one only ever iterates
 * `AUDIT_ENTITY_TYPES` itself, so the stricter key type is available and
 * catches a future seventh value at compile time instead of at a missing
 * label.
 */
const entityLabelKeys: Record<AuditEntityType, string> = {
	domain: 'audit.entityDomain',
	folder: 'audit.entityFolder',
	link: 'audit.entityLink',
	tag: 'audit.entityTag',
	team: 'audit.entityTeam',
	team_member: 'audit.entityTeamMember',
};

/**
 * Narrows a native `<select>`'s raw string value to `AuditEntityType`.
 * `audit-filters.ts` has its own version of this check but doesn't export
 * it — that one guards against an arbitrary search-parameter value, while
 * this one only ever sees a value this component put there itself, via the
 * options built from `AUDIT_ENTITY_TYPES` below. Duplicating the one-line
 * check is cheaper than exporting a guard for a second, differently-trusted
 * caller.
 *
 * @param value - The select's raw value.
 * @returns Whether it is one of the known entity types.
 */
function isAuditEntityType(value: string): value is AuditEntityType {
	return (AUDIT_ENTITY_TYPES as readonly string[]).includes(value);
}

/**
 * Applies one changed field on top of the current filters, always resetting
 * `page` to `1` — every control shares this rule, per CLAUDE.md: a filtered
 * view still showing a page number from the unfiltered result is a reader
 * looking at an empty page for no visible reason. `patch`'s three fields are
 * each `undefined` to mean "remove this filter", never an empty string —
 * `parseAuditFilters`'s own `AuditFilters` never carries one either.
 *
 * @param filters - The filters as they were before this change.
 * @param patch - The one field that changed, or all three left at their current value when only one is meant to change.
 * @param patch.actor - The chosen actor id, or `undefined` to clear it.
 * @param patch.entityType - The chosen entity type, or `undefined` to clear it.
 * @param patch.from - The chosen first day, or `undefined` to clear it.
 * @param patch.to - The chosen last day, or `undefined` to clear it.
 * @returns The next filters to report through `onChange`.
 */
function nextFilters(
	filters: Readonly<AuditFilters>,
	patch: Readonly<Partial<Pick<AuditFilters, 'actor' | 'entityType' | 'from' | 'to'>>>,
): AuditFilters {
	const merged = { ...filters, ...patch };

	return {
		...(merged.actor !== undefined && { actor: merged.actor }),
		...(merged.entityType !== undefined && { entityType: merged.entityType }),
		...(merged.from !== undefined && { from: merged.from }),
		page: 1,
		...(merged.to !== undefined && { to: merged.to }),
	};
}

export interface AuditFilterBarProps {
	/** The filters as they currently live in the route's search parameters. */
	readonly filters: AuditFilters;
	/** The team's current members, offered as the "Person" filter's choices. */
	readonly members: readonly Readonly<{ email: string; user_id: string }>[];
	/** Called with the whole next `AuditFilters`, page already reset to `1`. */
	readonly onChange: (filters: AuditFilters) => void;
}

/**
 * The audit log's filter bar: entity type, actor and a from/to day range,
 * plus a "clear the filters" button that only appears once a filter is set.
 * Presentational, like `AuditEntryTable` — it neither fetches nor owns the
 * filters, it only reports the reader's choices upward.
 *
 * Both selects are `NativeSelect` rather than the design system's
 * popup-based `Select`, for the same reason `link-form.tsx`'s
 * `redirect_type` field is: it keeps `userEvent.selectOptions` working and
 * keeps the control keyboard- and screen-reader-complete without a second
 * implementation. The day bounds are plain `<Input type="date">`s rather
 * than a calendar popover for the reason `StatRangePicker` doesn't fit
 * here: these are two independent bounds with no presets and no retention
 * floor, and the browser's own date control is already complete for that.
 *
 * @param props - The component's props.
 * @param props.filters - The filters as they currently live in the route's search parameters.
 * @param props.members - The team's current members, offered as the "Person" filter's choices.
 * @param props.onChange - Called with the whole next `AuditFilters`, page already reset to `1`.
 * @returns The rendered filter bar.
 */
export function AuditFilterBar({
	filters,
	members,
	onChange,
}: AuditFilterBarProps): React.JSX.Element {
	const { t } = useTranslation();
	const entityId = useId();
	const actorId = useId();
	const fromId = useId();
	const toId = useId();

	// Shared with the page's own choice between its two empty states — see
	// `hasActiveFilters`'s docstring for why the two must not be separate
	// copies of the same four-way check.
	const hasFilters = hasActiveFilters(filters);

	return (
		<div>
			<FieldGroup>
				<Field>
					<FieldLabel htmlFor={entityId}>{t('audit.filterEntity')}</FieldLabel>
					<NativeSelect
						id={entityId}
						onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
							const { value } = event.target;
							onChange(
								nextFilters(filters, {
									entityType: value !== '' && isAuditEntityType(value) ? value : undefined,
								}),
							);
						}}
						value={filters.entityType ?? ''}
					>
						<NativeSelectOption value="">{t('audit.filterEntityAny')}</NativeSelectOption>
						{AUDIT_ENTITY_TYPES.map((entityType) => (
							<NativeSelectOption key={entityType} value={entityType}>
								{t(entityLabelKeys[entityType])}
							</NativeSelectOption>
						))}
					</NativeSelect>
				</Field>

				<Field>
					<FieldLabel htmlFor={actorId}>{t('audit.filterActor')}</FieldLabel>
					<NativeSelect
						id={actorId}
						onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
							const { value } = event.target;
							onChange(nextFilters(filters, { actor: value === '' ? undefined : value }));
						}}
						value={filters.actor ?? ''}
					>
						<NativeSelectOption value="">{t('audit.filterActorAny')}</NativeSelectOption>
						{members.map((member) => (
							<NativeSelectOption key={member.user_id} value={member.user_id}>
								{member.email}
							</NativeSelectOption>
						))}
					</NativeSelect>
				</Field>

				<Field>
					<FieldLabel htmlFor={fromId}>{t('audit.filterFrom')}</FieldLabel>
					<Input
						id={fromId}
						onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
							const { value } = event.target;
							onChange(nextFilters(filters, { from: value === '' ? undefined : value }));
						}}
						type="date"
						value={filters.from ?? ''}
					/>
				</Field>

				<Field>
					<FieldLabel htmlFor={toId}>{t('audit.filterTo')}</FieldLabel>
					<Input
						id={toId}
						onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
							const { value } = event.target;
							onChange(nextFilters(filters, { to: value === '' ? undefined : value }));
						}}
						type="date"
						value={filters.to ?? ''}
					/>
				</Field>
			</FieldGroup>

			{hasFilters ? (
				<Button
					onClick={() => {
						onChange({ page: 1 });
					}}
					type="button"
					variant="outline"
				>
					{t('audit.filterReset')}
				</Button>
			) : null}
		</div>
	);
}
