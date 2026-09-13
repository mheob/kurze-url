import { useForm } from '@tanstack/react-form';
import { useTranslation } from 'react-i18next';

import { Button } from './ui/button';

const defaultValues: LinkFormValues = {
	analytics_enabled: true,
	destination_url: '',
	domain_id: '',
	expires_at: '',
	redirect_type: 302,
	slug: '',
};

/**
 * Every field name this form renders an inline error for. A server field
 * error naming anything outside this set (a field this form has no input
 * for at all — e.g. one the API grows later) has nowhere to attach, and
 * without a fallback it would silently vanish instead of surfacing. See the
 * generic alert rendered below for those.
 */
/** The redirect status code CLAUDE.md's "301 vs 302" note warns about: cached by browsers, so clicks go uncounted and destination changes stop taking effect. */
const REDIRECT_PERMANENT = 301;

const KNOWN_FIELD_NAMES: ReadonlySet<string> = new Set([
	'analytics_enabled',
	'destination_url',
	'domain_id',
	'expires_at',
	'redirect_type',
	'slug',
]);

interface LinkFormProps {
	// A team's *verified* domains, or undefined/empty when there are none to
	// offer — either way the picker below renders nothing at all, per its own
	// docstring: a `<select>` with a single, forced option is furniture, not a
	// choice.
	readonly domains?: readonly Readonly<{ id: string; hostname: string }>[];
	readonly fieldErrors?: Readonly<Record<string, string>>;
	readonly initial?: Partial<LinkFormValues>;
	readonly onSubmit: (values: LinkFormValues) => void;
}

export interface LinkFormValues {
	readonly analytics_enabled: boolean;
	readonly destination_url: string;
	readonly domain_id: string;
	readonly expires_at: string;
	readonly redirect_type: number;
	readonly slug: string;
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
 *
 * @param props - The component's props.
 * @param props.domains - The team's verified domains; the domain picker renders nothing when this is empty or undefined.
 * @param props.fieldErrors - Server-reported field errors, keyed by field name.
 * @param props.initial - Initial values to seed the form from, for the edit route.
 * @param props.onSubmit - Called with the form's values on submit.
 * @returns The rendered form.
 */
export function LinkForm({
	domains,
	fieldErrors,
	initial,
	onSubmit,
}: LinkFormProps): React.JSX.Element {
	const { t } = useTranslation();

	const form = useForm({
		defaultValues: { ...defaultValues, ...initial },
		onSubmit: ({ value }: { readonly value: LinkFormValues }) => {
			onSubmit(value);
		},
	});

	// A server error naming a field this form doesn't render (see
	// `KNOWN_FIELD_NAMES` above) — surfaced as a generic alert rather than
	// nowhere at all.
	const unhandledFieldErrors = fieldErrors
		? Object.entries(fieldErrors).filter(
				([name]: readonly [string, string]) => !KNOWN_FIELD_NAMES.has(name),
			)
		: [];

	return (
		<form
			onSubmit={(event: Readonly<{ preventDefault: () => void; stopPropagation: () => void }>) => {
				event.preventDefault();
				event.stopPropagation();
				void form.handleSubmit();
			}}
		>
			{unhandledFieldErrors.length > 0 ? (
				<p role="alert">
					{unhandledFieldErrors.map(([, message]: readonly [string, string]) => message).join(' ')}
				</p>
			) : null}

			<form.Field
				name="destination_url"
				validators={{
					onChange: ({ value }: { readonly value: string }) =>
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
								aria-describedby={errorMessage !== undefined ? errorId : undefined}
								aria-invalid={errorMessage !== undefined ? true : undefined}
								id="destination_url"
								name={field.name}
								onBlur={field.handleBlur}
								onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
									field.handleChange(event.target.value);
								}}
								required
								type="url"
								value={field.state.value}
							/>
							{errorMessage !== undefined ? (
								<p id={errorId} role="alert">
									{errorMessage}
								</p>
							) : null}
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
								aria-describedby={errorMessage !== undefined ? errorId : undefined}
								aria-invalid={errorMessage !== undefined ? true : undefined}
								id="slug"
								name={field.name}
								onBlur={field.handleBlur}
								onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
									field.handleChange(event.target.value);
								}}
								placeholder={t('links.slugGenerated')}
								value={field.state.value}
							/>
							{errorMessage !== undefined ? (
								<p id={errorId} role="alert">
									{errorMessage}
								</p>
							) : null}
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
								aria-describedby={errorMessage !== undefined ? errorId : undefined}
								aria-invalid={errorMessage !== undefined ? true : undefined}
								id="redirect_type"
								name={field.name}
								onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
									field.handleChange(Number(event.target.value));
								}}
								value={field.state.value}
							>
								<option value={302}>{t('links.redirect302')}</option>
								<option value={301}>{t('links.redirect301')}</option>
							</select>
							{errorMessage !== undefined ? (
								<p id={errorId} role="alert">
									{errorMessage}
								</p>
							) : null}
							{/* CLAUDE.md requires this. A cached 301 stops clicks being counted
							    and stops later destination changes taking effect for anyone who
							    has already visited — breakage a volunteer cannot diagnose and
							    cannot undo. It belongs next to the choice, not in a tooltip. */}
							{field.state.value === REDIRECT_PERMANENT ? (
								<p role="note">{t('links.redirect301Warning')}</p>
							) : null}
						</div>
					);
				}}
			</form.Field>

			<form.Field name="expires_at">
				{(field) => {
					const errorId = 'expires_at-error';
					const errorMessage = fieldErrors?.expires_at;

					return (
						<div>
							<label htmlFor="expires_at">{t('links.expiresAt')}</label>
							<input
								aria-describedby={errorMessage !== undefined ? errorId : undefined}
								aria-invalid={errorMessage !== undefined ? true : undefined}
								id="expires_at"
								name={field.name}
								onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
									field.handleChange(event.target.value);
								}}
								type="datetime-local"
								value={field.state.value}
							/>
							{errorMessage !== undefined ? (
								<p id={errorId} role="alert">
									{errorMessage}
								</p>
							) : null}
						</div>
					);
				}}
			</form.Field>

			<form.Field name="analytics_enabled">
				{(field) => {
					const errorId = 'analytics_enabled-error';
					const errorMessage = fieldErrors?.analytics_enabled;

					return (
						<div>
							<label htmlFor="analytics_enabled">{t('links.analyticsEnabled')}</label>
							<input
								aria-describedby={errorMessage !== undefined ? errorId : undefined}
								aria-invalid={errorMessage !== undefined ? true : undefined}
								checked={field.state.value}
								id="analytics_enabled"
								name={field.name}
								onChange={(event: Readonly<{ target: Readonly<{ checked: boolean }> }>) => {
									field.handleChange(event.target.checked);
								}}
								type="checkbox"
							/>
							{errorMessage !== undefined ? (
								<p id={errorId} role="alert">
									{errorMessage}
								</p>
							) : null}
						</div>
					);
				}}
			</form.Field>

			{/* Furniture check: a select offering only the shared domain is no
			    choice at all, so this renders nothing unless the team has at
			    least one verified domain to pick instead. The empty-valued
			    option is the shared instance hostname — `toRequestBody` in the
			    create route maps `''` back to `undefined`, exactly as it already
			    does for `slug`/`expires_at`, so leaving this untouched keeps
			    today's behaviour. */}
			{domains && domains.length > 0 ? (
				<form.Field name="domain_id">
					{(field) => {
						const errorId = 'domain_id-error';
						const errorMessage = fieldErrors?.domain_id;

						return (
							<div>
								<label htmlFor="domain_id">{t('links.domain')}</label>
								<select
									aria-describedby={errorMessage !== undefined ? errorId : undefined}
									aria-invalid={errorMessage !== undefined ? true : undefined}
									id="domain_id"
									name={field.name}
									onChange={(event: Readonly<{ target: Readonly<{ value: string }> }>) => {
										field.handleChange(event.target.value);
									}}
									value={field.state.value}
								>
									<option value="">{t('links.domainShared')}</option>
									{domains.map((domain) => (
										<option key={domain.id} value={domain.id}>
											{domain.hostname}
										</option>
									))}
								</select>
								{errorMessage !== undefined ? (
									<p id={errorId} role="alert">
										{errorMessage}
									</p>
								) : null}
							</div>
						);
					}}
				</form.Field>
			) : null}

			<Button type="submit">{t('links.save')}</Button>
		</form>
	);
}
