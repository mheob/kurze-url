import type { Domain as ApiDomain, VerifyDomainOutputBody } from '@kurze-url/api-client';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import { DomainList } from './domain-list';

type VerifyReason = VerifyDomainOutputBody['reason'];

function domain(overrides: Partial<ApiDomain> = {}): ApiDomain {
	return {
		hostname: 'links.verein.test',
		id: 'domain-1',
		records: {
			cname: { name: 'links.verein.test', value: 'cname.vercel-dns.com' },
			txt: { name: '_kurze-url-challenge.links.verein.test', value: 'tok-a' },
		},
		team_id: 'team-a',
		verification_status: 'pending',
		verification_token: 'tok-a',
		verified_at: null,
		...overrides,
	};
}

const pendingDomain = domain();
const verifiedDomain = domain({
	verification_status: 'verified',
	verified_at: '2026-01-01T00:00:00Z',
});

interface RenderOverrides {
	readonly deleteBlockedCount?: number;
	readonly deletingId?: string | null;
	readonly onDelete?: (domainId: string) => void;
	readonly pendingReason?: VerifyReason;
	readonly verifyPending?: boolean;
	readonly verifyingId?: string | null;
}

/**
 * No router in this tree, unlike `link-list.test.tsx`'s `renderWith`:
 * `DomainList` renders no `<Link>` at all — there is no per-domain detail
 * page to navigate to, only inline `onVerify`/`onDelete` callbacks — so it
 * needs nothing beyond an `I18nextProvider`.
 */
function renderList(
	domains: ApiDomain[],
	overrides: RenderOverrides = {},
): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<DomainList
				deleteBlockedCount={overrides.deleteBlockedCount}
				deletingId={overrides.deletingId ?? null}
				domains={domains}
				onDelete={overrides.onDelete ?? vi.fn()}
				onVerify={vi.fn()}
				pendingReason={overrides.pendingReason}
				verifyPending={overrides.verifyPending}
				verifyingId={overrides.verifyingId ?? null}
			/>
		</I18nextProvider>,
	);
}

describe('DomainList', () => {
	it('shows both DNS records for a pending domain', () => {
		// A Verein that cannot see what to put in DNS cannot proceed, and this
		// is the only screen that tells them.
		renderList([pendingDomain]);

		expect(screen.getByText('_kurze-url-challenge.links.verein.test')).toBeInTheDocument();
		expect(screen.getByText('tok-a')).toBeInTheDocument();
		expect(screen.getByText('cname.vercel-dns.com')).toBeInTheDocument();
	});

	it('labels which record is TXT and which is CNAME', () => {
		// The record type is the first thing a Verein must pick from their
		// registrar's dropdown before they can enter anything at all — nothing
		// else on this screen said "TXT" or "CNAME" before this fix, only an
		// inference from the name/value shapes a non-technical reader cannot
		// be expected to make.
		renderList([pendingDomain]);

		expect(screen.getByText('TXT')).toBeInTheDocument();
		expect(screen.getByText('CNAME')).toBeInTheDocument();
	});

	it('gives each record value copy button a distinct accessible name', () => {
		// Two `CopyButton`s in the same row, each labelled plain "Copy", are
		// ambiguous to anyone tabbing through them rather than reading the
		// row visually.
		renderList([pendingDomain]);

		expect(screen.getByRole('button', { name: 'Copy the TXT record value' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Copy the CNAME record value' })).toBeInTheDocument();
	});

	it('hides the records once the domain works', () => {
		renderList([verifiedDomain]);
		expect(screen.queryByText(/_kurze-url-challenge/)).not.toBeInTheDocument();
	});

	it('explains which half of verification is missing', () => {
		// The reason is the whole reason the endpoint returns one. Showing a
		// bare "not verified" would leave a Verein guessing between a DNS typo
		// and a step that is not theirs to take. `verifyingId` is what scopes
		// `pendingReason` to this specific domain — see `DomainList`'s own
		// docstring for why a single reason slot is enough for this screen.
		renderList([pendingDomain], { pendingReason: 'unreachable', verifyingId: pendingDomain.id });
		expect(screen.getByText(/does not reach us yet/i)).toBeInTheDocument();
	});

	it('explains a missing TXT record', () => {
		// The switch in `reasonLabel` is mechanical across all three reasons,
		// but only `unreachable` had a text-level assertion — `token_missing`
		// and `token_mismatch` were an inherited gap from the brief's own
		// illustrative test, not something to leave uncovered now it is
		// noticed.
		renderList([pendingDomain], {
			pendingReason: 'token_missing',
			verifyingId: pendingDomain.id,
		});
		expect(screen.getByText(/txt record is not visible yet/i)).toBeInTheDocument();
	});

	it('explains a TXT record whose value does not match', () => {
		renderList([pendingDomain], {
			pendingReason: 'token_mismatch',
			verifyingId: pendingDomain.id,
		});
		expect(screen.getByText(/its value does not match/i)).toBeInTheDocument();
	});

	it('does not explain a reason that belongs to a different domain', () => {
		// `verifyingId` matches neither rendered domain here — the reason must
		// not be misattributed to one it was never about.
		const other = domain({ id: 'domain-2', hostname: 'other.verein.test' });
		renderList([pendingDomain, other], {
			pendingReason: 'unreachable',
			verifyingId: 'domain-3',
		});
		expect(screen.queryByText(/does not reach us yet/i)).not.toBeInTheDocument();
	});

	it('puts the records in a table, not in divs', () => {
		// Accessibility is a CI gate at two levels. A grid of divs passes a
		// visual review and fails axe.
		renderList([pendingDomain]);
		expect(screen.getAllByRole('row').length).toBeGreaterThan(1);
	});

	it('shows the empty state when the team has no domains', () => {
		renderList([]);
		expect(screen.getByText(/no domains yet/i)).toBeInTheDocument();
	});

	it('shows a status per domain, including a losing claim', () => {
		const failed = domain({ id: 'domain-3', verification_status: 'failed' });
		renderList([failed]);
		expect(screen.getByText(/another team verified this hostname first/i)).toBeInTheDocument();
		expect(screen.queryByRole('button', { name: 'Check now' })).not.toBeInTheDocument();
	});

	it('offers a Check now button for every pending domain', () => {
		renderList([pendingDomain]);
		expect(screen.getByRole('button', { name: 'Check now' })).toBeInTheDocument();
	});

	it('disables Check now while a verify for that domain is in flight', () => {
		// A second click before the first response lands must not fire a
		// second, overlapping verify request for the same domain.
		renderList([pendingDomain], { verifyingId: pendingDomain.id, verifyPending: true });
		expect(screen.getByRole('button', { name: 'Check now' })).toBeDisabled();
	});

	it('does not disable a different domain while another one is verifying', () => {
		// `verifyPending` is a single slot, correlated by `verifyingId` — the
		// same discipline `pendingReason`/`deleteBlockedCount` already follow.
		const other = domain({ id: 'domain-2', hostname: 'other.verein.test' });
		renderList([pendingDomain, other], { verifyingId: pendingDomain.id, verifyPending: true });
		const buttons = screen.getAllByRole('button', { name: 'Check now' });
		expect(buttons[0]).toBeDisabled();
		expect(buttons[1]).toBeEnabled();
	});

	it('gives each domain a distinct, hostname-naming delete button', () => {
		// A bare "Delete" repeated on every row is ambiguous to anyone tabbing
		// through them rather than reading the row visually — the same
		// reasoning that gave the two DNS copy buttons distinct names.
		const other = domain({ id: 'domain-2', hostname: 'other.verein.test' });
		renderList([pendingDomain, other]);

		expect(screen.getByRole('button', { name: 'Delete links.verein.test' })).toBeInTheDocument();
		expect(screen.getByRole('button', { name: 'Delete other.verein.test' })).toBeInTheDocument();
	});

	it('calls onDelete with the id of the domain that was actually confirmed', async () => {
		// Two domains rendered, and the *second* row confirmed — a test that
		// always fires on the first row cannot pass by accident.
		const onDelete = vi.fn();
		const other = domain({ id: 'domain-2', hostname: 'other.verein.test' });
		renderList([pendingDomain, other], { onDelete });

		await userEvent.click(screen.getByRole('button', { name: 'Delete other.verein.test' }));
		await userEvent.click(screen.getByRole('button', { name: 'Yes, delete it' }));

		expect(onDelete).toHaveBeenCalledExactlyOnceWith('domain-2');
	});

	it('requires confirmation before onDelete fires', async () => {
		// Nothing restores a deleted domain, and an empty one is gone for good —
		// one misclick must not be enough.
		const onDelete = vi.fn();
		renderList([pendingDomain], { onDelete });

		await userEvent.click(screen.getByRole('button', { name: 'Delete links.verein.test' }));
		expect(onDelete).not.toHaveBeenCalled();
	});

	it('renders the blocking-link count on a 409, not a generic failure', () => {
		// The count is the whole point of the refusal — it tells the team how
		// much work removing the domain is.
		renderList([pendingDomain], { deleteBlockedCount: 3, deletingId: pendingDomain.id });
		expect(
			screen.getByText('This domain still has 3 links. Delete them first.'),
		).toBeInTheDocument();
	});

	it('renders the singular wording for exactly one blocking link', () => {
		renderList([pendingDomain], { deleteBlockedCount: 1, deletingId: pendingDomain.id });
		expect(screen.getByText('This domain still has 1 link. Delete it first.')).toBeInTheDocument();
	});

	it('does not attribute a blocking-link count to a domain it was never about', () => {
		// `deletingId` matches neither rendered domain here — the same
		// correlation discipline `verifyingId`/`pendingReason` already follow.
		const other = domain({ id: 'domain-2', hostname: 'other.verein.test' });
		renderList([pendingDomain, other], { deleteBlockedCount: 3, deletingId: 'domain-3' });
		expect(screen.queryByText(/still has 3 links/i)).not.toBeInTheDocument();
	});
});
