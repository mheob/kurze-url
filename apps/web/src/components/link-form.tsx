import { useForm } from '@tanstack/react-form';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

export interface LinkFormValues {
	analytics_enabled: boolean;
	destination_url: string;
	expires_at: string;
	redirect_type: number;
	slug: string;
}

const defaultValues: LinkFormValues = {
	analytics_enabled: true,
	destination_url: '',
	expires_at: '',
	redirect_type: 302,
	slug: '',
};

interface LinkFormProps {
	readonly fieldErrors?: Readonly<Record<string, string>>;
	readonly initial?: Partial<LinkFormValues>;
	readonly onSubmit: (values: LinkFormValues) => void;
}

/**
 * Shared by the create route (Task 10) and, per the plan's pre-flight scan,
 * the edit route (Task 11) — `initial` is what lets the same component seed
 * itself from an existing `Link` instead of starting blank.
 *
 * Uses `@tanstack/react-form`'s `useForm`/`form.Field`, not the plain
 * `useState` the plan's own sample code showed: CLAUDE.md and this task's own
 * "Validation stays thin" instruction are explicit that TanStack Form covers
 * required-field/shape checks only, and that a zod schema mirroring the API's
 * rules must not be added — `internal/destination`'s SSRF and DNS-rebinding
 * checks cannot be reproduced in a browser at all, so a client schema that
 * looked authoritative would be the more dangerous kind of wrong. The one
 * client-side check here is "destination is required", surfaced through
 * `form.Field`'s own `onChange` validator rather than a parallel schema.
 */
export function LinkForm({ fieldErrors, initial, onSubmit }: LinkFormProps): React.JSX.Element {
	const { t } = useTranslation();

	const form = useForm({
		defaultValues: { ...defaultValues, ...initial },
		onSubmit: ({ value }) => onSubmit(value),
	});

	return (
		<form
			onSubmit={(event) => {
				event.preventDefault();
				event.stopPropagation();
				void form.handleSubmit();
			}}
		>
			<form.Field
				name="destination_url"
				validators={{
					onChange: ({ value }) =>
						value.trim() === '' ? t('links.destinationRequired') : undefined,
				}}
			>
				{(field) => {
					const errorId = 'destination_url-error';
					const errorMessage =
						fieldErrors?.destination_url ??
						(field.state.meta.isTouched ? field.state.meta.errors[0] : undefined);

					return (
						<div>
							<label htmlFor="destination_url">{t('links.destination')}</label>
							<input
								aria-describedby={errorMessage ? errorId : undefined}
								id="destination_url"
								name={field.name}
								onBlur={field.handleBlur}
								onChange={(event) => field.handleChange(event.target.value)}
								required
								type="url"
								value={field.state.value}
							/>
							{errorMessage ? <p id={errorId}>{errorMessage}</p> : null}
						</div>
					);
				}}
			</form.Field>

			<form.Field name="slug">
				{(field) => {
					const errorId = 'slug-error';
					const errorMessage = fieldErrors?.slug;

					return (
						<div>
							<label htmlFor="slug">{t('links.slug')}</label>
							{/* An empty slug means the API generates one. Said here, because a
							    blank required-looking field otherwise reads as an oversight. */}
							<input
								aria-describedby={errorMessage ? errorId : undefined}
								id="slug"
								name={field.name}
								onBlur={field.handleBlur}
								onChange={(event) => field.handleChange(event.target.value)}
								placeholder={t('links.slugGenerated')}
								value={field.state.value}
							/>
							{errorMessage ? <p id={errorId}>{errorMessage}</p> : null}
						</div>
					);
				}}
			</form.Field>

			<form.Field name="redirect_type">
				{(field) => {
					const errorId = 'redirect_type-error';
					const errorMessage = fieldErrors?.redirect_type;

					return (
						<div>
							<label htmlFor="redirect_type">{t('links.redirectType')}</label>
							<select
								aria-describedby={errorMessage ? errorId : undefined}
								id="redirect_type"
								name={field.name}
								onChange={(event) => field.handleChange(Number(event.target.value))}
								value={field.state.value}
							>
								<option value={302}>{t('links.redirect302')}</option>
								<option value={301}>{t('links.redirect301')}</option>
							</select>
							{errorMessage ? <p id={errorId}>{errorMessage}</p> : null}
							{/* CLAUDE.md requires this. A cached 301 stops clicks being counted
							    and stops later destination changes taking effect for anyone who
							    has already visited — breakage a volunteer cannot diagnose and
							    cannot undo. It belongs next to the choice, not in a tooltip. */}
							{field.state.value === 301 ? (
								<p role="note">{t('links.redirect301Warning')}</p>
							) : null}
						</div>
					);
				}}
			</form.Field>

			<form.Field name="expires_at">
				{(field) => (
					<div>
						<label htmlFor="expires_at">{t('links.expiresAt')}</label>
						<input
							id="expires_at"
							name={field.name}
							onChange={(event) => field.handleChange(event.target.value)}
							type="datetime-local"
							value={field.state.value}
						/>
					</div>
				)}
			</form.Field>

			<form.Field name="analytics_enabled">
				{(field) => (
					<div>
						<label htmlFor="analytics_enabled">{t('links.analyticsEnabled')}</label>
						<input
							checked={field.state.value}
							id="analytics_enabled"
							name={field.name}
							onChange={(event) => field.handleChange(event.target.checked)}
							type="checkbox"
						/>
					</div>
				)}
			</form.Field>

			{/* No domain picker: one domain exists on this instance, so a select
			    with one option would be furniture. It appears when custom domains
			    do — see `links.new.tsx` in a later plan. */}

			<Button type="submit">{t('links.save')}</Button>
		</form>
	);
}
