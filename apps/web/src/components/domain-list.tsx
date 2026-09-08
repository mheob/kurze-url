import type { Domain, VerifyDomainOutputBody } from '@kurze-url/api-client';
import type { TFunction } from 'i18next';
import { useTranslation } from 'react-i18next';

import { ConfirmDelete } from './confirm-delete';
import { CopyButton } from './copy-button';
import { Button } from './ui/button';

type VerifyReason = VerifyDomainOutputBody['reason'];

interface DomainListProps {
	readonly domains: readonly Domain[];
	// Set only for whichever domain `deletingId` names — same one-slot
	// correlation `pendingReason`/`verifyingId` already use below, and for the
	// same reason: only one delete can be in flight at a time.
	readonly deleteBlockedCount?: number;
	readonly deletingId: string | null;
	readonly onDelete: (domainId: string) => void;
	readonly onVerify: (domainId: string) => void;
	// Set only for whichever domain `verifyingId` names — see the docstring
	// below for why one slot, not a per-domain map, is enough here.
	readonly pendingReason?: VerifyReason;
	// True while the verify call named by `verifyingId` is still in flight.
	// Disables that row's "Check now" button so a click that lands before the
	// first response comes back cannot fire a second, overlapping verify
	// request for the same domain.
	readonly verifyPending?: boolean;
	readonly verifyingId: string | null;
}

/**
 * `verification_status` is a plain `string` on the wire (`Domain`'s
 * generated type), not the narrower union this UI actually knows about — an
 * unrecognised value is echoed back rather than silently dropped, so a
 * status this screen doesn't yet handle surfaces instead of disappearing.
 */
function statusLabel(t: TFunction, status: string): string {
	switch (status) {
		case 'pending': {
			return t('domains.pending');
		}
		case 'verified': {
			return t('domains.verified');
		}
		case 'failed': {
			return t('domains.failed');
		}
		default: {
			return status;
		}
	}
}

/**
 * Exhaustive over `VerifyReason` — the enum tag Task 9 added to
 * `VerifyDomainOutput.Body.Reason` specifically so this can switch instead of
 * falling back to a generic "not verified" message. The `''` case is the
 * empty-string success value a later contract fix added to the enum
 * (`domainverify.ReasonNone`, echoed back unchanged on the success path) —
 * this function is only ever called from behind a truthy `pendingReason`
 * check at the call site below, so `''` never actually reaches here, but the
 * union includes it now and the switch stays exhaustive rather than leaning
 * on `default` for a value the type already names. `default` itself is kept
 * for every value this union does *not* yet admit — it narrows `reason` to
 * `never` today, so it only ever runs — echoing the raw value, the same
 * fallback `statusLabel` above uses — if a later reason is added here before
 * its catalogue entry exists.
 */
function reasonLabel(t: TFunction, reason: VerifyReason): string {
	switch (reason) {
		case '': {
			return reason;
		}
		case 'token_missing': {
			return t('domains.reason.token_missing');
		}
		case 'token_mismatch': {
			return t('domains.reason.token_mismatch');
		}
		case 'unreachable': {
			return t('domains.reason.unreachable');
		}
		default: {
			return reason;
		}
	}
}

/**
 * Presentational and prop-driven, the same contract as `LinkList`: it takes
 * the already-fetched domains as a prop rather than calling
 * `useSuspenseQuery` itself, and callbacks (`onVerify`, `onDelete`) for the
 * actions it offers rather than owning a mutation. `teams.$teamSlug.domains.tsx`
 * is the only caller, wiring this to the query cache and the verify/delete
 * mutations; `domain-list.test.tsx` renders it directly with hand-built
 * `Domain` fixtures instead.
 *
 * `pendingReason` is scoped to a single domain via `verifyingId`, and
 * `deleteBlockedCount` the same way via `deletingId`, rather than either
 * being a per-domain map: a team's domain list is expected to stay small
 * (`server/domains.ts`'s own reasoning for omitting pagination), the route
 * only ever runs one verify check or one delete at a time, and `verifyingId`/
 * `deletingId` already name which domain that action was for — so one slot
 * each, correlated by id, is enough. Neither is ever rendered under a row
 * whose id doesn't match; it is silently ignored for every other row rather
 * than misattributed to one it was never about.
 *
 * DNS records and the "Check now" button render only for a `pending`
 * domain: a `verified` one has nothing left to check (Task 13's own
 * requirement — instructions are not decoration once the domain works), and
 * a `failed` one lost the hostname to another team's earlier verification,
 * so re-checking the same DNS records could never change the outcome. The
 * delete control has no such restriction — every domain, regardless of
 * status, can be removed; only a domain that still has links refuses (409),
 * which is what `deleteBlockedCount` surfaces.
 */
export function DomainList({
	deleteBlockedCount,
	deletingId,
	domains,
	onDelete,
	onVerify,
	pendingReason,
	verifyPending,
	verifyingId,
}: DomainListProps): React.JSX.Element {
	const { t } = useTranslation();

	if (domains.length === 0) return <p>{t('domains.empty')}</p>;

	return (
		<>
			<h1>{t('domains.heading')}</h1>
			<ul>
				{domains.map((domain) => (
					<li key={domain.id}>
						<h2>{domain.hostname}</h2>
						{/* `<dl>`, not a literal ": " between two spans: the label/value
						    pairing is expressed structurally, so no punctuation has to be
						    hardcoded to join them. */}
						<dl>
							<dt>{t('domains.status')}</dt>
							<dd>{statusLabel(t, domain.verification_status)}</dd>
						</dl>
						{domain.verification_status === 'pending' ? (
							<>
								<h3>{t('domains.recordsHeading')}</h3>
								<table>
									<thead>
										<tr>
											<th scope="col">{t('domains.recordType')}</th>
											<th scope="col">{t('domains.recordName')}</th>
											<th scope="col">{t('domains.recordValue')}</th>
										</tr>
									</thead>
									<tbody>
										<tr>
											<td>{t('domains.recordTypeTxt')}</td>
											<td>{domain.records.txt.name}</td>
											<td>
												{domain.records.txt.value}
												<CopyButton
													label={t('domains.copyTxtValue')}
													value={domain.records.txt.value}
												/>
											</td>
										</tr>
										<tr>
											<td>{t('domains.recordTypeCname')}</td>
											<td>{domain.records.cname.name}</td>
											<td>
												{domain.records.cname.value}
												<CopyButton
													label={t('domains.copyCnameValue')}
													value={domain.records.cname.value}
												/>
											</td>
										</tr>
									</tbody>
								</table>
								{verifyingId === domain.id && pendingReason ? (
									<output>{reasonLabel(t, pendingReason)}</output>
								) : null}
								<Button
									disabled={verifyingId === domain.id && verifyPending}
									onClick={() => onVerify(domain.id)}
									type="button"
								>
									{t('domains.verify')}
								</Button>
							</>
						) : null}
						{deletingId === domain.id && deleteBlockedCount !== undefined ? (
							<output>{t('domains.deleteBlockedByLinks', { count: deleteBlockedCount })}</output>
						) : null}
						<ConfirmDelete
							label={t('domains.delete', { hostname: domain.hostname })}
							onConfirm={() => onDelete(domain.id)}
							question={t('domains.deleteQuestion', { hostname: domain.hostname })}
						/>
					</li>
				))}
			</ul>
		</>
	);
}
