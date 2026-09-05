import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

interface ConfirmDeleteProps {
	readonly label: string;
	readonly onConfirm: () => void;
}

/**
 * One misclick must not delete a link: nothing restores it, and its slug may
 * already be printed on a flyer or a poster. The first click only arms the
 * control; a second, explicit click is what actually calls `onConfirm`.
 *
 * `role="alertdialog"` needs an accessible name to mean anything to a screen
 * reader — the plan's own sample rendered a bare `<p role="alertdialog">`
 * with no `aria-label`/`aria-labelledby`, which is exactly the "proper
 * semantics and an accessible name" this task's own instructions call out.
 * `useId()` (not a hardcoded id) is what keeps this safe to render more than
 * once on the same page — e.g. one `ConfirmDelete` per row in a future list
 * view — without two instances colliding on the same id.
 */
export function ConfirmDelete({ label, onConfirm }: ConfirmDeleteProps): React.JSX.Element {
	const { t } = useTranslation();
	const [armed, setArmed] = useState(false);
	const questionId = useId();

	if (!armed) {
		return (
			<Button onClick={() => setArmed(true)} type="button">
				{label}
			</Button>
		);
	}

	return (
		<div aria-labelledby={questionId} role="alertdialog">
			<p id={questionId}>{t('links.deleteQuestion')}</p>
			<Button onClick={onConfirm} type="button">
				{t('links.deleteConfirm')}
			</Button>
			<Button onClick={() => setArmed(false)} type="button">
				{t('links.cancel')}
			</Button>
		</div>
	);
}
