/* oxlint-disable typescript/prefer-readonly-parameter-types -- `StatsWindow` and `StatRange` are
 * the shapes this component is handed and hands back: `StatRange` is generated
 * `@kurze-url/api-client` output, and `StatsWindow` is the type `stat-range-picker.tsx`'s own
 * `onChange` already takes unwrapped. Marking them readonly here alone would make one caller's
 * signature disagree with the other's for no gain. Same note as that file's.
 */

import type { StatRange } from '@kurze-url/api-client';
import { useTranslation } from 'react-i18next';

import { formatDay } from '../lib/format';
import type { Language } from '../lib/preferences';
import type { StatsWindow } from '../lib/stats-window';
import { Button } from './ui/button';

export interface StatRecordedJumpProps {
	/** The active language, for formatting the two dates. */
	readonly language: Language;
	/** Called with the recorded range, so the page can navigate to it. */
	readonly onSelect: (window: StatsWindow) => void;
	/** The range the API reported, already bounded by the retention floor. */
	readonly recorded: StatRange;
}

/**
 * Offers the window a link actually has data for.
 *
 * It exists because the alternative is advice: an empty window cannot say
 * whether widening it would help, and the page used to guess ("Try a longer
 * window") at both readers it could not tell apart. The endpoint now reports
 * the answer, and the useful form of an answer here is a window the reader can
 * take rather than a hint they have to translate into two dates.
 *
 * The range is handed back exactly as it arrived. The API bounded it by the
 * retention floor so that a range it reports is always one it can serve;
 * rebuilding a window from these dates locally would discard that guarantee.
 *
 * @param props - The component's props.
 * @param props.language - The active language, for formatting the two dates.
 * @param props.onSelect - Called with the recorded range when the reader presses the button.
 * @param props.recorded - The range the API reported.
 * @returns The rendered button.
 */
export function StatRecordedJump({
	language,
	onSelect,
	recorded,
}: StatRecordedJumpProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<Button
			onClick={() => {
				onSelect({ from: recorded.from, to: recorded.to });
			}}
			type="button"
		>
			{t('stats.showRecorded', {
				from: formatDay(recorded.from, language),
				to: formatDay(recorded.to, language),
			})}
		</Button>
	);
}
