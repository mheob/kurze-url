import { useId, useState } from 'react';
import { useTranslation } from 'react-i18next';

import {
	validateLinkPassword,
	type LinkPasswordContext,
	type LinkPasswordReason,
} from '../lib/link-password';
import { ConfirmDelete } from './confirm-delete';
import { Button } from './ui/button';

export interface LinkPasswordCardProps {
	readonly context: LinkPasswordContext;
	readonly hasPassword: boolean;
	readonly onRemove: () => void;
	readonly onSet: (password: string) => void;
	/** A reason the API returned that the mirrored policy did not predict. */
	readonly rejection?: LinkPasswordReason | 'rejected';
}

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

/**
 * Deliberately not a field inside `<LinkForm>`: that form maps to `PATCH`,
 * which excludes `password` on purpose, and the value is write-only — the
 * server holds an Argon2id hash and cannot return the current password, so
 * there is no initial value to seed a controlled input with. A permanently
 * blank field inside a form that otherwise round-trips the link's current
 * state would read as "the password is empty", which is why this renders
 * beside `<LinkForm>` instead.
 *
 * "Card" is a role, not a component this app has a primitive for — plain
 * `<form>`/`<label>`/`<input>` plus `Button`, the same shape `link-form.tsx`
 * uses, including its `errorId`/`aria-describedby`/`aria-invalid` wiring.
 *
 * `validateLinkPassword` runs first, client-side, against `context` — the
 * same policy the API enforces — so a password derived from this link, its
 * destination, or the Verein's name is refused immediately, without a round
 * trip. `rejection` is the escape hatch for the reverse case: a reason the
 * mirror missed (or a token this build doesn't recognise, `'rejected'`) that
 * only the API caught, still rendered through the exact same message table
 * so it reaches the reader either way.
 */
export function LinkPasswordCard({
	context,
	hasPassword,
	onRemove,
	onSet,
	rejection,
}: LinkPasswordCardProps): React.JSX.Element {
	const { t } = useTranslation();
	const inputId = useId();
	const errorId = useId();

	const [password, setPassword] = useState('');
	// The reason the *last local check* found, distinct from `rejection` (the
	// API's own finding): a fresh keystroke clears this so a stale local
	// message doesn't linger over a password the reader has already changed.
	const [localReason, setLocalReason] = useState<LinkPasswordReason | null>(null);
	// Protecting a link starts with the input visible; once protected, it
	// stays hidden until the reader explicitly asks to change the password —
	// so a submit can't happen by accident on a link that is already secured.
	const [changing, setChanging] = useState(false);

	const reason = localReason ?? rejection;
	const message = reason ? t(messageKeys[reason]) : undefined;

	function handleSubmit(event: React.FormEvent<HTMLFormElement>): void {
		event.preventDefault();
		const violation = validateLinkPassword(password, context);
		setLocalReason(violation);
		if (violation === null) onSet(password);
	}

	const submitLabel = t(hasPassword ? 'links.passwordChange' : 'links.passwordProtect');

	const passwordInput = (
		<form onSubmit={handleSubmit}>
			<div>
				<label htmlFor={inputId}>{t('links.passwordLabel')}</label>
				<input
					aria-describedby={message ? errorId : undefined}
					aria-invalid={message ? true : undefined}
					autoComplete="new-password"
					id={inputId}
					onChange={(event) => {
						setPassword(event.target.value);
						setLocalReason(null);
					}}
					type="password"
					value={password}
				/>
				{message ? (
					<p id={errorId} role="alert">
						{message}
					</p>
				) : null}
			</div>
			<Button type="submit">{submitLabel}</Button>
		</form>
	);

	return (
		<section>
			<h2>{t('links.passwordHeading')}</h2>
			<p>{t('links.passwordExplainer')}</p>
			<p>{t(hasPassword ? 'links.passwordProtected' : 'links.passwordUnprotected')}</p>

			{hasPassword && !changing ? (
				<>
					<Button onClick={() => setChanging(true)} type="button">
						{t('links.passwordChange')}
					</Button>
					<ConfirmDelete
						label={t('links.passwordRemove')}
						onConfirm={onRemove}
						question={t('links.passwordRemoveQuestion')}
					/>
				</>
			) : (
				passwordInput
			)}
		</section>
	);
}
