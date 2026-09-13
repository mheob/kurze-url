import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
	validateLinkPassword,
	type LinkPasswordContext,
	type LinkPasswordReason,
} from '../lib/link-password';
import { ConfirmDelete } from './confirm-delete';
import { Button } from './ui/button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from './ui/card';
import { Field, FieldError, FieldLabel } from './ui/field';
import { Input } from './ui/input';

/**
 * Maps every reason `validateLinkPassword` (Task 7) or the API's typed 422
 * detail (Task 8's `passwordRejected`) can carry to its translation key, as a
 * `Record` rather than a lookup function — adding a reason to
 * `LinkPasswordReason` without adding it here is a compile error, not a blank
 * message a reader would have no way to act on.
 */
const messageKeys: Record<LinkPasswordReason | 'rejected', string> = {
	derived_from_context: 'links.passwordDerivedFromContext',
	rejected: 'links.passwordRejected',
	too_common: 'links.passwordTooCommon',
	too_long: 'links.passwordTooLong',
	too_repetitive: 'links.passwordTooRepetitive',
	too_short: 'links.passwordTooShort',
};

export interface LinkPasswordCardProps {
	readonly context: LinkPasswordContext;
	readonly hasPassword: boolean;
	/**
	 * Called when the reader edits the field, so a stale API-reported
	 * `rejection` does not linger over a password they are in the middle of
	 * correcting. Optional: `rejection` is state the parent owns, so a caller
	 * with none of it set has nothing to clear.
	 */
	readonly onDismissRejection?: () => void;
	readonly onRemove: () => void;
	/**
	 * Resolves on a successful set/change, rejects on failure. The card owns
	 * `changing`/`password` and has no other way to learn which one
	 * happened, so it awaits this call: a resolved promise closes the editor
	 * and clears the field, a rejected one leaves both exactly as the reader
	 * left them. The parent still classifies *why* a failure happened and
	 * feeds a policy rejection back through `rejection` below — this return
	 * value only carries success-or-not, never the reason.
	 */
	readonly onSet: (password: string) => Promise<void>;
	/** A reason the API returned that the mirrored policy did not predict. */
	readonly rejection?: LinkPasswordReason | 'rejected';
}

/**
 * Deliberately not a field inside `<LinkForm>`: that form maps to `PATCH`,
 * which excludes `password` on purpose, and the value is write-only — the
 * server holds an Argon2id hash and cannot return the current password, so
 * there is no initial value to seed a controlled input with. A permanently
 * blank field inside a form that otherwise round-trips the link's current
 * state would read as "the password is empty", which is why this renders
 * beside `<LinkForm>` instead.
 *
 * Wrapped in the design system's `Card`, with the password field itself built
 * from `Field`/`FieldLabel`/`FieldError` plus `Button` — the same idiom
 * `link-form.tsx` uses, including its `errorId`/`aria-describedby`/
 * `aria-invalid` wiring.
 *
 * `validateLinkPassword` runs first, client-side, against `context` — the
 * same policy the API enforces — so a password derived from this link, its
 * destination, or the Verein's name is refused immediately, without a round
 * trip. `rejection` is the escape hatch for the reverse case: a reason the
 * mirror missed (or a token this build doesn't recognise, `'rejected'`) that
 * only the API caught, still rendered through the exact same message table
 * so it reaches the reader either way.
 *
 * @param props - The component's props.
 * @param props.context - The link/destination/team values the mirrored password policy checks against.
 * @param props.hasPassword - Whether the link currently has a password.
 * @param props.onDismissRejection - Called when the reader edits the field, to clear a stale `rejection`.
 * @param props.onRemove - Removes the link's password.
 * @param props.onSet - Sets or changes the link's password; resolves on success, rejects on failure.
 * @param props.rejection - A reason the API returned that the mirrored policy did not predict.
 * @returns The rendered password card section.
 */
export function LinkPasswordCard({
	context,
	hasPassword,
	onDismissRejection,
	onRemove,
	onSet,
	rejection,
}: LinkPasswordCardProps): React.JSX.Element {
	const { t } = useTranslation();
	const inputId = useId();
	const errorId = useId();

	const [password, setPassword] = useState('');
	// The reason the *last local check* found, distinct from `rejection` (the
	// API's own finding): a fresh keystroke clears this, and calls
	// `onDismissRejection` to ask the parent to clear its half too, so a
	// stale message doesn't linger over a password the reader has already
	// changed.
	const [localReason, setLocalReason] = useState<LinkPasswordReason | null>(null);
	// Protecting a link starts with the input visible; once protected, it
	// stays hidden until the reader explicitly asks to change the password —
	// so a submit can't happen by accident on a link that is already secured.
	const [changing, setChanging] = useState(false);

	const reason = localReason ?? rejection;
	const message = reason ? t(messageKeys[reason]) : undefined;

	/**
	 * Async so a successful `onSet` can close the editor and clear the field
	 * from right here — the one place that knows both "the mirrored policy
	 * passed" and "the server accepted it". A rejection leaves `password`
	 * and `changing` untouched: the reader's just-typed value stays in the
	 * field so they can edit and resubmit, and the parent's `rejection` prop
	 * (or its own banner, for a failure that isn't a policy rejection at
	 * all) is what tells them why.
	 *
	 * @param event - The form's submit event; prevented immediately so the mirrored policy check runs before any network call.
	 */
	async function handleSubmit(event: Readonly<{ preventDefault: () => void }>): Promise<void> {
		event.preventDefault();
		const violation = validateLinkPassword(password, context);
		setLocalReason(violation);
		if (violation !== null) return;

		try {
			await onSet(password);
			setPassword('');
			setLocalReason(null);
			setChanging(false);
		} catch {
			// Rejected: keep the field open with its value. Nothing else to do
			// here — the parent already re-renders with an updated `rejection`
			// prop, or its own banner, depending on what classifyApiError found.
		}
	}

	const submitLabel = t(hasPassword ? 'links.passwordChange' : 'links.passwordProtect');
	// A cancel control only makes sense once there is a "back" to go to — the
	// initial, unprotected-link input has no prior state, but opening
	// "Change password" on an already-protected link does, and until now
	// there was no way back out of it short of a submit.
	const showCancel = hasPassword && changing;

	function handleCancel(): void {
		setChanging(false);
		setPassword('');
		setLocalReason(null);
	}

	// `void`, not a bare `onSubmit={handleSubmit}`: the handler is async, and
	// React ignores the promise it returns. Discarding it explicitly says that
	// is intended rather than overlooked, which is what `no-misused-promises`
	// asks for. Nothing is lost by it — the only await inside is already
	// wrapped in its own try/catch, so the promise cannot reject.
	const passwordInput = (
		<form onSubmit={(event: Readonly<{ preventDefault: () => void }>) => void handleSubmit(event)}>
			<Field data-invalid={message !== undefined}>
				<FieldLabel htmlFor={inputId}>{t('links.passwordLabel')}</FieldLabel>
				<Input
					aria-describedby={message !== undefined ? errorId : undefined}
					aria-invalid={message !== undefined ? true : undefined}
					autoComplete="new-password"
					id={inputId}
					onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
						setPassword(event.target.value);
						setLocalReason(null);
						onDismissRejection?.();
					}}
					type="password"
					value={password}
				/>
				{message === undefined ? null : <FieldError id={errorId}>{message}</FieldError>}
			</Field>
			<Button type="submit">{submitLabel}</Button>
			{showCancel ? (
				<Button onClick={handleCancel} type="button">
					{t('links.cancel')}
				</Button>
			) : null}
		</form>
	);

	return (
		<Card>
			<CardHeader>
				{/* `CardTitle` hardcodes a `<div>` — it has no `render`/`asChild` prop
				    to hand it a real heading tag, and `role="heading"` on a `<div>`
				    is exactly what `jsx-a11y/prefer-tag-over-role` refuses. Nesting a
				    real `<h2>` keeps the section in the page's heading structure
				    without either problem: Tailwind's preflight resets a heading's
				    font-size/weight to `inherit`, so `CardTitle`'s own classes still
				    style it, unchanged from before this section was a `Card`. */}
				<CardTitle>
					<h2>{t('links.passwordHeading')}</h2>
				</CardTitle>
				<CardDescription>{t('links.passwordExplainer')}</CardDescription>
			</CardHeader>
			<CardContent>
				<p>{t(hasPassword ? 'links.passwordProtected' : 'links.passwordUnprotected')}</p>

				{hasPassword && !changing ? (
					<>
						<Button
							onClick={() => {
								setChanging(true);
							}}
							type="button"
						>
							{t('links.passwordChange')}
						</Button>
						<ConfirmDelete
							confirmLabel={t('links.passwordRemoveConfirm')}
							label={t('links.passwordRemove')}
							onConfirm={onRemove}
							question={t('links.passwordRemoveQuestion')}
						/>
					</>
				) : (
					passwordInput
				)}
			</CardContent>
		</Card>
	);
}
