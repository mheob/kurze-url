import type { Domain as ApiDomain, VerifyDomainOutputBody } from '@kurze-url/api-client';
import { render, screen } from '@testing-library/react';
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

/**
 * No router in this tree, unlike `link-list.test.tsx`'s `renderWith`:
 * `DomainList` renders no `<Link>` at all — there is no per-domain detail
 * page to navigate to, only an inline `onVerify` callback — so it needs
 * nothing beyond an `I18nextProvider`.
 */
function renderList(
	domains: ApiDomain[],
	overrides: { pendingReason?: VerifyReason; verifyingId?: string | null } = {},
): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<DomainList
				domains={domains}
				onVerify={vi.fn()}
				pendingReason={overrides.pendingReason}
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
});
