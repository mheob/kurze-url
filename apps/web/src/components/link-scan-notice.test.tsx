import { act, render, screen } from '@testing-library/react';
import { I18nextProvider } from 'react-i18next';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { createI18n } from '../i18n';
import type { Language } from '../lib/preferences';
import { ADVISORY_URL, REPORT_ERROR_URL, WARNING_WINDOW_MS } from '../lib/safe-browsing';
import { flaggedScan } from '../test/link-scan';
import { LinkScanNotice } from './link-scan-notice';

/**
 * Same pattern as `audit-entry-table.test.tsx`: a component calling
 * `useTranslation` needs an `I18nextProvider` or `t(...)` throws.
 *
 * @param ui - The element under test.
 * @param language - Which catalogue to load.
 * @returns Testing Library's render result.
 */
function renderWithI18n(
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `React.ReactElement` is React's own type.
	ui: React.ReactElement,
	language: Language = 'en',
): ReturnType<typeof render> {
	return render(<I18nextProvider i18n={createI18n(language)}>{ui}</I18nextProvider>);
}

describe(LinkScanNotice, () => {
	it('renders nothing for a link that is not blocked', () => {
		const { container } = renderWithI18n(<LinkScanNotice state="active" />);
		expect(container).toBeEmptyDOMElement();
	});

	it("goes by the link's state, not the scan: a link that is not blocked shows nothing", () => {
		// A scan can outlive the block it described, so a flagged verdict beside
		// an active link must not put a warning on the page.
		const { container } = renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['MALWARE'])} state="active" />,
		);
		expect(container).toBeEmptyDOMElement();
	});

	it("goes by the link's state, not the scan: a blocked link beside a clean verdict says only that it is blocked", () => {
		// Redirects refuse on `state`, so the page says the link is blocked even
		// when the scan next to it reads clean, but a clean verdict confirms no
		// threat, so it gets the neutral notice rather than Google's warning. A
		// clean verdict carries no `since` when it has held since the first
		// check, which is normal.
		renderWithI18n(
			<LinkScanNotice
				scan={{ checked_at: new Date().toISOString(), threat_types: [], verdict: 'clean' }}
				state="flagged"
			/>,
		);

		expect(
			screen.getByRole('region', { name: 'Blocked until the next check' }),
		).toBeInTheDocument();
		expect(screen.queryByText(/Google Safe Browsing reports/u)).not.toBeInTheDocument();
	});

	it("names a suspected phishing site and links Google's definition of it", () => {
		renderWithI18n(<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING'])} state="flagged" />);

		// A labelled region, not an alert: it is on the page on every visit
		// while the block lasts, and is not news on any of them.
		expect(screen.getByRole('region', { name: 'Suspected phishing site' })).toBeInTheDocument();
		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'What Google means by phishing' })).toHaveAttribute(
			'href',
			'https://developers.google.com/search/docs/monitor-debug/security/social-engineering',
		);
	});

	it('credits Google, admits it can be wrong, and says what to do next', () => {
		renderWithI18n(<LinkScanNotice scan={flaggedScan(['MALWARE'])} state="flagged" />);

		expect(screen.getByRole('link', { name: 'Advisory provided by Google' })).toHaveAttribute(
			'href',
			ADVISORY_URL,
		);
		expect(screen.getByText(/cannot guarantee/u)).toBeInTheDocument();
		expect(
			screen.getByRole('link', { name: 'Report an incorrect warning to Google' }),
		).toHaveAttribute('href', REPORT_ERROR_URL);
		expect(screen.getByText(/re-check every 30 minutes/u)).toBeInTheDocument();
	});

	it('gives a mix of threats the generic heading and every category its sentence', () => {
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING', 'MALWARE'])} state="flagged" />,
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Suspected unsafe site' }),
		).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'What Google means by phishing' })).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'What Google means by malware' })).toBeInTheDocument();
	});

	it('links each of two types whose definition is the same page', () => {
		// Google defines malware and unwanted software on one page; both labels
		// still appear, and sharing an address must not collapse them into one.
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['MALWARE', 'UNWANTED_SOFTWARE'])} state="flagged" />,
		);

		expect(screen.getByRole('link', { name: 'What Google means by malware' })).toBeInTheDocument();
		expect(
			screen.getByRole('link', { name: 'What Google means by unwanted software' }),
		).toBeInTheDocument();
	});

	it('still explains a threat type Google added later', () => {
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['THREAT_TYPE_FROM_THE_FUTURE'])} state="flagged" />,
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Suspected unsafe site' }),
		).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'About Google Safe Browsing' })).toBeInTheDocument();
	});

	it('shows the generic qualified text when Google named no threat type', () => {
		renderWithI18n(<LinkScanNotice scan={flaggedScan([])} state="flagged" />);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Suspected unsafe site' }),
		).toBeInTheDocument();
		expect(screen.getByText(/may be unsafe/u)).toBeInTheDocument();
		expect(screen.getByRole('link', { name: 'About Google Safe Browsing' })).toBeInTheDocument();
	});

	it('shows the generic text, not an error, when the API sent null threat types', () => {
		// `threat_types` is generated as `Array<string> | null`, so a flagged link
		// whose scan row names nothing must not take the page down.
		renderWithI18n(
			<LinkScanNotice
				scan={{ checked_at: new Date().toISOString(), threat_types: null, verdict: 'flagged' }}
				state="flagged"
			/>,
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Suspected unsafe site' }),
		).toBeInTheDocument();
	});

	it('still says the link is blocked while no verdict has been loaded', () => {
		renderWithI18n(<LinkScanNotice state="flagged" />);

		expect(
			screen.getByRole('region', { name: 'Blocked until the next check' }),
		).toBeInTheDocument();
		expect(screen.getByText(/This link is blocked until/u)).toBeInTheDocument();
	});

	describe("the thirty-minute rule Google's terms set", () => {
		afterEach(() => {
			vi.useRealTimers();
		});

		it("drops Google's warning once the last check is 29 minutes old", () => {
			// Google's terms allow a warning only on information younger than
			// thirty minutes. A sweep that runs late, or Google being down, leaves
			// the flag older than that, and the page must then say no more than
			// that the link is blocked.
			const checkedAt = new Date(Date.now() - WARNING_WINDOW_MS).toISOString();
			renderWithI18n(
				<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING'], checkedAt)} state="flagged" />,
			);

			expect(
				screen.getByRole('region', { name: 'Blocked until the next check' }),
			).toBeInTheDocument();
			expect(screen.queryByText(/phishing/iu)).not.toBeInTheDocument();
			expect(screen.queryByRole('link')).not.toBeInTheDocument();
			expect(screen.getByText(/lifts the block/u)).toBeInTheDocument();
		});

		it("keeps Google's warning just inside the window", () => {
			const checkedAt = new Date(Date.now() - WARNING_WINDOW_MS + 60_000).toISOString();
			renderWithI18n(
				<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING'], checkedAt)} state="flagged" />,
			);

			expect(screen.getByRole('region', { name: 'Suspected phishing site' })).toBeInTheDocument();
		});

		it('turns neutral while the page stays open', () => {
			// The page is loaded once and may stay open for an hour; the warning
			// must not outlive the check behind it just because nothing re-rendered.
			vi.useFakeTimers({ now: new Date('2026-10-17T08:00:00.000Z') });
			renderWithI18n(
				<LinkScanNotice
					scan={flaggedScan(['MALWARE'], '2026-10-17T08:00:00.000Z')}
					state="flagged"
				/>,
			);
			expect(screen.getByRole('region', { name: 'Possibly harmful software' })).toBeInTheDocument();

			act(() => {
				vi.advanceTimersByTime(WARNING_WINDOW_MS);
			});

			expect(
				screen.getByRole('region', { name: 'Blocked until the next check' }),
			).toBeInTheDocument();
			expect(screen.queryByText(/harmful/iu)).not.toBeInTheDocument();
		});

		it('is German', () => {
			renderWithI18n(<LinkScanNotice state="flagged" />, 'de');

			expect(
				screen.getByRole('region', { name: 'Gesperrt bis zur nächsten Prüfung' }),
			).toBeInTheDocument();
		});
	});

	it('is German', () => {
		renderWithI18n(
			<LinkScanNotice scan={flaggedScan(['SOCIAL_ENGINEERING'])} state="flagged" />,
			'de',
		);

		expect(
			screen.getByRole('heading', { level: 2, name: 'Mutmaßliche Phishing-Seite' }),
		).toBeInTheDocument();
		expect(
			screen.getByRole('link', { name: 'Von Google bereitgestellte Hinweise' }),
		).toBeInTheDocument();
		expect(
			screen.getByRole('link', { name: 'Eine falsche Warnung an Google melden' }),
		).toBeInTheDocument();
	});
});
