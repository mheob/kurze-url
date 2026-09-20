/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
   file is the same `(field) => {...}` render-prop parameter TanStack Form's `form.Field` supplies,
   the same pattern `link-form.tsx` disables the rule for: reconstructing that type by hand to mark
   it readonly was tried there and reverted after a nested field came out subtly wrong. Every other
   handler in this file (the `<form>`'s own `onSubmit`, each input's `onChange`) types its DOM event
   parameter explicitly as a `Readonly<{...}>` instead, the same as that file does. */

import { useForm } from '@tanstack/react-form';
import { useEffect, useId } from 'react';
import { useTranslation } from 'react-i18next';

import { isTeamRole, type TeamRole } from '../lib/team-roles';
import { Button } from './ui/button';
import { Field, FieldDescription, FieldError, FieldGroup, FieldLabel } from './ui/field';
import { Input } from './ui/input';
import { NativeSelect, NativeSelectOption } from './ui/native-select';

interface InviteFormValues {
	readonly email: string;
	readonly role: TeamRole;
}

/**
 * Only catches an obvious typo before a round trip — `apps/api`'s own
 * `format:"email"` validation is the real check, the same division of labour
 * `link-form.tsx`'s doc comment describes for its own one client-side rule.
 */
/* oxlint-disable-next-line regexp/no-super-linear-backtracking -- the two `[^\s@]+` runs around the
   literal dot are what the task brief specifies verbatim; an email address is a short, bounded
   input, so the polynomial worst case this rule warns about is not a usable attack surface here,
   and the API's own `format:"email"` validation is the actual check this one exists to shortcut. */
const EMAIL_SHAPE = /^[^\s@]+@[^\s@]+\.[^\s@]+$/u;

/** Keyed by `TeamRole` so the option list below cannot name a role with no catalogue entry. */
const ROLE_LABEL_KEYS: Readonly<Record<TeamRole, string>> = {
	admin: 'members.roleAdmin',
	editor: 'members.roleEditor',
	owner: 'members.roleOwner',
	viewer: 'members.roleViewer',
};

/**
 * Maps a failure to the catalogue key describing it, exhaustively over
 * `InviteFailureKind` by construction: the `default` case narrows `failure`
 * to `never`, so a ninth reason added without an arm above it fails to
 * compile instead of silently reaching `t()` as `undefined`.
 *
 * @param failure - The reason the server refused, or silently skipped notifying, the last invite.
 * @returns The catalogue key naming that failure.
 */
function failureMessageKey(failure: InviteFailureKind): string {
	switch (failure) {
		case 'alreadyMember': {
			return 'members.errorAlreadyMember';
		}
		case 'instanceBudget': {
			return 'members.errorInstanceBudget';
		}
		case 'mailFailed': {
			return 'members.errorMailFailed';
		}
		case 'notConfigured': {
			return 'members.errorNotConfigured';
		}
		case 'raced': {
			return 'members.errorRaced';
		}
		case 'teamBurst': {
			return 'members.errorTeamBurst';
		}
		case 'unknown': {
			return 'errors.unknown';
		}
		default: {
			const exhaustive: never = failure;
			return exhaustive;
		}
	}
}

/**
 * Every reason the server can refuse — or silently decline to notify anyone
 * about — the last invite this form submitted. Task 7's mutation hook
 * classifies the API's response into one of these before this form ever sees
 * it; each gets its own `members.error*` copy because "something went wrong"
 * would flatten an instance-wide mail budget problem into the same sentence
 * as a plain duplicate-member refusal.
 */
export type InviteFailureKind =
	| 'alreadyMember'
	| 'instanceBudget'
	| 'mailFailed'
	| 'notConfigured'
	| 'raced'
	| 'teamBurst'
	| 'unknown';

export interface MemberInviteFormProps {
	readonly failure: InviteFailureKind | null;
	readonly onSubmit: (values: { readonly email: string; readonly role: TeamRole }) => void;
	readonly pending: boolean;
	/** Empty for a member who may not invite; the form renders nothing then. */
	readonly roles: readonly TeamRole[];
	/** The last successful add, so the form can say whether an email went out. */
	readonly result: { readonly email: string; readonly invited: boolean } | null;
}

/**
 * The invite form on the members page: an email address, a role select, and
 * the honest reporting of what the server did with them — whether the last
 * add actually sent an invitation (`result`) or the attempt was refused
 * (`failure`). Renders nothing for a member without the rank to invite
 * (`roles` empty), the same "a picker with nothing to pick is furniture, not
 * a choice" rule `link-form.tsx`'s domain picker already follows.
 *
 * Built on `@tanstack/react-form`'s `useForm`, exactly as `link-form.tsx` is:
 * validation stays thin — a required check plus one regex to catch an
 * obvious typo, never a parallel copy of the API's own rules — and every
 * field gets its own `useId()` rather than a hardcoded id, so a second
 * instance of this form on one page cannot collide with the first.
 *
 * @param props - The component's props.
 * @param props.failure - The reason the last submitted invite failed, or `null`.
 * @param props.onSubmit - Called with the trimmed email and chosen role on submit.
 * @param props.pending - Disables the submit button while the invite request is in flight.
 * @param props.roles - The roles this caller may assign, ascending; empty renders nothing.
 * @param props.result - The last successful add, so the form can report whether it notified anyone.
 * @returns The rendered form, or `null` for a caller who may not invite.
 */
export function MemberInviteForm({
	failure,
	onSubmit,
	pending,
	roles,
	result,
}: MemberInviteFormProps): React.JSX.Element | null {
	const { t } = useTranslation();
	const emailId = useId();
	const emailErrorId = useId();
	const emailHintId = useId();
	const roleId = useId();

	const initialValues: InviteFormValues = { email: '', role: roles[0] ?? 'viewer' };
	const form = useForm({
		defaultValues: initialValues,
		onSubmit: ({ value }: { readonly value: InviteFormValues }) => {
			onSubmit({ email: value.email.trim(), role: value.role });
		},
	});

	// Resets the address and role back to their defaults after a successful
	// add: `result` only ever flips from `null` to a value on success (a
	// refused invite leaves it `null` — see `clearStatusSlots` in the route),
	// so this never fires for a rejection and a caller fixing a typo does not
	// lose what they typed. Without it, the address stays in the field under
	// the "Invitation sent" banner and the button re-enables, so a second,
	// accidental click spends another unit of the instance's own invitation
	// budget (`RATE_LIMIT_INVITE_GLOBAL_PER_MONTH`) on a guaranteed 409.
	// `link-form.tsx` gets away without this only because its route navigates
	// away on success; this form's route does not.
	useEffect(() => {
		if (result !== null) form.reset();
		// `form` is safe to list: `useForm` memoises the object it returns for
		// this component's lifetime, so including it never causes an extra run.
	}, [form, result]);

	if (roles.length === 0) return null;

	return (
		<form
			onSubmit={(event: Readonly<{ preventDefault: () => void; stopPropagation: () => void }>) => {
				event.preventDefault();
				event.stopPropagation();
				void form.handleSubmit();
			}}
		>
			<h2>{t('members.inviteHeading')}</h2>

			{result === null ? null : (
				<output>
					{result.invited
						? t('members.invitedSent', { email: result.email })
						: t('members.invitedAddedSilently', { email: result.email })}
				</output>
			)}

			{failure === null ? null : <p role="alert">{t(failureMessageKey(failure))}</p>}

			<FieldGroup>
				<form.Field
					name="email"
					validators={{
						onChange: ({ value }: { readonly value: string }) => {
							const trimmed = value.trim();
							if (trimmed === '') return t('members.inviteEmailRequired');
							if (!EMAIL_SHAPE.test(trimmed)) return t('members.inviteEmailInvalid');
							return undefined;
						},
					}}
				>
					{(field) => {
						const errorMessage = field.state.meta.isTouched
							? field.state.meta.errors[0]
							: undefined;
						const describedBy =
							errorMessage === undefined ? emailHintId : `${emailHintId} ${emailErrorId}`;

						return (
							<Field data-invalid={errorMessage !== undefined}>
								<FieldLabel htmlFor={emailId}>{t('members.inviteEmail')}</FieldLabel>
								<Input
									aria-describedby={describedBy}
									aria-invalid={errorMessage === undefined ? undefined : true}
									id={emailId}
									name={field.name}
									onBlur={field.handleBlur}
									onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
										field.handleChange(event.target.value);
									}}
									type="email"
									value={field.state.value}
								/>
								<FieldDescription id={emailHintId}>{t('members.inviteEmailHint')}</FieldDescription>
								{errorMessage === undefined ? null : (
									<FieldError id={emailErrorId}>{errorMessage}</FieldError>
								)}
							</Field>
						);
					}}
				</form.Field>

				<form.Field name="role">
					{(field) => (
						<Field>
							<FieldLabel htmlFor={roleId}>{t('members.inviteRole')}</FieldLabel>
							<NativeSelect
								id={roleId}
								name={field.name}
								onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
									if (isTeamRole(event.target.value)) field.handleChange(event.target.value);
								}}
								value={field.state.value}
							>
								{roles.map((role) => (
									<NativeSelectOption key={role} value={role}>
										{t(ROLE_LABEL_KEYS[role])}
									</NativeSelectOption>
								))}
							</NativeSelect>
						</Field>
					)}
				</form.Field>
			</FieldGroup>

			<Button disabled={pending} type="submit">
				{t('members.inviteSubmit')}
			</Button>
		</form>
	);
}
