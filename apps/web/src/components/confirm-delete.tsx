import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

interface ConfirmDeleteProps {
	/**
	 * Fully rendered string, not a translation key — the same convention
	 * `label` and `question` already use. Defaults to `links.deleteConfirm`
	 * ("Yes, delete it") when omitted; see the docstring below for when to
	 * override it.
	 */
	readonly confirmLabel?: string;
	readonly label: string;
	readonly onConfirm: () => void;
	readonly question: string;
}

/**
 * One misclick must not delete something: nothing restores a link, and a
 * removed domain has to be re-verified from scratch. The first click only
 * arms the control; a second, explicit click is what actually calls
 * `onConfirm`.
 *
 * `question` is a fully rendered string, not a translation key — the same
 * convention `label` already used — because the consequence of confirming
 * differs by what is being deleted (a link's short URL 404ing for everyone
 * who has it; a domain losing its verification) and only the caller knows
 * which. This component was link-only until the team domains screen became
 * its second caller; `links.deleteConfirm`/`links.cancel` stayed as they
 * were, since "Yes, delete it"/"Cancel" apply to any deletion and needed no
 * per-caller wording, the same reason `CopyButton`'s visible "Copy" text
 * stayed put when *its* `label` prop was added for a second caller.
 *
 * That reasoning covered two callers who both genuinely delete an entity. It
 * stopped covering a third: removing a link's password deletes nothing, and
 * "Yes, delete it" on that control reads as an offer to delete the *link* —
 * exactly the misclick this component exists to prevent. `confirmLabel` is
 * the fix, optional and defaulted to `links.deleteConfirm` so both existing
 * callers are unaffected: the default stays generic because most callers of
 * this component do delete something, and a caller whose action is not a
 * deletion overrides it with its own fully rendered string, on the same
 * "only the caller knows which" reasoning as `question`.
 *
 * `role="alertdialog"` needs an accessible name to mean anything to a screen
 * reader — the plan's own sample rendered a bare `<p role="alertdialog">`
 * with no `aria-label`/`aria-labelledby`, which is exactly the "proper
 * semantics and an accessible name" this task's own instructions call out.
 * `useId()` (not a hardcoded id) is what keeps this safe to render more than
 * once on the same page — one `ConfirmDelete` per row in a list view —
 * without two instances colliding on the same id.
 */
export function ConfirmDelete({
	confirmLabel,
	label,
	onConfirm,
	question,
}: ConfirmDeleteProps): React.JSX.Element {
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
			<p id={questionId}>{question}</p>
			<Button onClick={onConfirm} type="button">
				{confirmLabel ?? t('links.deleteConfirm')}
			</Button>
			<Button onClick={() => setArmed(false)} type="button">
				{t('links.cancel')}
			</Button>
		</div>
	);
}
