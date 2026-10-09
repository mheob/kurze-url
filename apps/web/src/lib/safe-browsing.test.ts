import { describe, expect, it } from 'vitest';

import {
	headingCategory,
	SAFE_BROWSING_URL,
	threatCategories,
	threatDefinitions,
} from './safe-browsing.ts';

describe(threatCategories, () => {
	it('groups the threat types Google reports into phishing and harmful software', () => {
		expect(threatCategories(['MALWARE', 'SOCIAL_ENGINEERING', 'UNWANTED_SOFTWARE'])).toStrictEqual([
			'phishing',
			'harmful',
		]);
	});

	it('reads a threat type it does not know as a generic threat', () => {
		// Google adds types over time; dropping one would leave a blocked link
		// with no reason on the page.
		expect(threatCategories(['THREAT_TYPE_FROM_THE_FUTURE'])).toStrictEqual(['unknown']);
	});

	it('keeps a type it does not know beside the ones it does', () => {
		// The block page does the same: the known type's section, then the
		// generic one, so the unknown type is not swallowed by its neighbour.
		expect(threatCategories(['MALWARE', 'THREAT_TYPE_FROM_THE_FUTURE'])).toStrictEqual([
			'harmful',
			'unknown',
		]);
	});

	it('falls back to the generic threat when Google named none', () => {
		expect(threatCategories([])).toStrictEqual(['unknown']);
	});
});

describe(headingCategory, () => {
	it('gives one category its own heading and a mix the generic one', () => {
		expect(headingCategory(['phishing'])).toBe('phishing');
		expect(headingCategory(['phishing', 'harmful'])).toBe('unknown');
	});
});

describe(threatDefinitions, () => {
	it("links each reported type to Google's own definition", () => {
		// Google's usage page defines malware and unwanted software on one page,
		// so both land there under their own labels, in a fixed order whatever
		// order Google reported them in.
		expect(threatDefinitions('harmful', ['UNWANTED_SOFTWARE', 'MALWARE'])).toStrictEqual([
			{
				labelKey: 'links.scanDefinitionMalware',
				url: 'https://developers.google.com/search/docs/monitor-debug/security/malware',
			},
			{
				labelKey: 'links.scanDefinitionUnwantedSoftware',
				url: 'https://developers.google.com/search/docs/monitor-debug/security/malware',
			},
		]);
	});

	it('links phishing and potentially harmful applications to their own pages', () => {
		expect(threatDefinitions('phishing', ['SOCIAL_ENGINEERING'])).toStrictEqual([
			{
				labelKey: 'links.scanDefinitionSocialEngineering',
				url: 'https://developers.google.com/search/docs/monitor-debug/security/social-engineering',
			},
		]);
		expect(threatDefinitions('harmful', ['POTENTIALLY_HARMFUL_APPLICATION'])).toStrictEqual([
			{
				labelKey: 'links.scanDefinitionHarmfulApplication',
				url: 'https://developers.google.com/android/play-protect/potentially-harmful-applications',
			},
		]);
	});

	it('leaves out the types of the other categories', () => {
		expect(threatDefinitions('phishing', ['SOCIAL_ENGINEERING', 'MALWARE'])).toHaveLength(1);
	});

	it('points a generic threat at Safe Browsing itself', () => {
		expect(threatDefinitions('unknown', ['THREAT_TYPE_FROM_THE_FUTURE'])).toStrictEqual([
			{ labelKey: 'links.scanDefinitionUnknown', url: SAFE_BROWSING_URL },
		]);
	});
});
