import { useTranslation } from 'react-i18next';

import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
	AlertDialogTrigger,
} from './ui/alert-dialog';
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
 * removed domain has to be re-verified from scratch. The trigger only opens
 * a confirmation dialog; only an explicit click on that dialog's own confirm
 * action calls `onConfirm`.
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
 * Built on `AlertDialog` rather than the hand-rolled `role="alertdialog"`
 * `<div>` this component used before: that version had the right role but
 * none of the behaviour a real dialog needs — no focus trap, no Escape
 * handling, and cancelling unmounted the trigger and restored focus to
 * nothing, dropping a keyboard user at the top of the document.
 * `AlertDialogTitle` both supplies the dialog's accessible name (from
 * `question`, the same value the old hand-rolled `aria-labelledby` pointed
 * at) and returns focus to the trigger on close, for free.
 *
 * @param props - The component's props.
 * @param props.confirmLabel - Fully rendered confirm-button string; defaults to "Yes, delete it" when omitted.
 * @param props.label - Fully rendered label for the trigger that opens the confirmation dialog.
 * @param props.onConfirm - Called when the dialog's own confirm action is clicked.
 * @param props.question - Fully rendered question shown in the confirmation dialog.
 * @returns The rendered trigger and its confirmation dialog.
 */
export function ConfirmDelete({
	confirmLabel,
	label,
	onConfirm,
	question,
}: ConfirmDeleteProps): React.JSX.Element {
	const { t } = useTranslation();

	return (
		<AlertDialog>
			{/* oxlint-disable-next-line react-perf/jsx-no-jsx-as-prop -- Base UI's `render`-prop composition idiom (`useRender`'s "Migrating from Radix UI" guide): this is the element `AlertDialogTrigger` clones and merges its own props onto. A stable reference would need a `useMemo` around a two-line static element per `ConfirmDelete` instance — one per row in a list view. */}
			<AlertDialogTrigger render={<Button type="button" />}>{label}</AlertDialogTrigger>
			<AlertDialogContent>
				<AlertDialogHeader>
					<AlertDialogTitle>{question}</AlertDialogTitle>
				</AlertDialogHeader>
				<AlertDialogFooter>
					<AlertDialogCancel type="button">{t('links.cancel')}</AlertDialogCancel>
					<AlertDialogAction onClick={onConfirm} type="button">
						{confirmLabel ?? t('links.deleteConfirm')}
					</AlertDialogAction>
				</AlertDialogFooter>
			</AlertDialogContent>
		</AlertDialog>
	);
}
