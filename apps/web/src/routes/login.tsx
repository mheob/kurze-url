import { createFileRoute, useSearch } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '../components/ui/button';
import { Card, CardContent, CardHeader, CardTitle } from '../components/ui/card';
import { Field, FieldLabel } from '../components/ui/field';
import { Input } from '../components/ui/input';
import { sendMagicLink } from '../server/auth';

// A fourth `'failed'` status, not a second boolean alongside `sent`: this
// form has never had more than one outcome on screen at once, and a status
// value is what keeps that true by construction — `sent` and `failed` both
// `true` at once was a reachable, meaningless state a second boolean would
// have allowed.
type SendStatus = 'failed' | 'idle' | 'pending' | 'sent';

interface LoginFormProps {
	/**
	 * Where the magic link should bring the visitor once it has signed them in: the consent page a
	 * CLI login started from. Passed on as it came, because the server function is the one that
	 * decides whether it is safe to follow.
	 */
	readonly next?: string;
}

/**
 * The sign-in form: an address in, a magic link out.
 *
 * @param props - The form's props.
 * @param props.next - Where the magic link should lead after sign-in, from the `next` search param.
 * @returns The form, inside a `<main>` landmark.
 */
export function LoginForm({ next }: LoginFormProps): React.JSX.Element {
	const { t } = useTranslation();
	const [status, setStatus] = useState<SendStatus>('idle');
	const [email, setEmail] = useState('');

	return (
		// `region` (axe) fails any content that sits outside every landmark,
		// and this page renders no header or footer of its own — the bare
		// `<form>` was the whole document. Same one-`<main>`-per-document rule
		// `_authed.tsx` satisfies for the authenticated tree.
		<main className="flex min-h-screen items-center justify-center bg-background px-6 py-12 text-foreground">
			<div className="w-full max-w-sm">
				<Card>
					<CardHeader>
						{/* `CardTitle` hardcodes a `<div>` — see the same note on
					    `link-password-card.tsx`'s `CardTitle`: a real nested `<h1>`
					    (the only heading this page has) keeps it in the page's
					    heading structure without fighting
					    `jsx-a11y/prefer-tag-over-role`, and without editing
					    `ui/card.tsx`. */}
						<CardTitle>
							<h1>{t('auth.signInTitle')}</h1>
						</CardTitle>
					</CardHeader>
					<CardContent>
						<form
							className="flex flex-col gap-6"
							// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- React's own `FormEvent` type; not a declaration this file can edit.
							onSubmit={(event) => {
								event.preventDefault();
								setStatus('pending');
								// `createSupabase` throws outright when `SUPABASE_URL` or
								// `SUPABASE_PUBLISHABLE_KEY` is unset (both new env vars this
								// branch introduces), and the RPC itself can reject on a plain
								// network failure — either way, an uncaught rejection here used to
								// leave the button looking clicked with no feedback, forever, on a
								// misconfigured deployment. `signInWithOtp`'s own failure is never
								// one of these: `sendMagicLinkFor` deliberately swallows it to keep
								// this form from becoming an account-enumeration oracle.
								void (async () => {
									try {
										await sendMagicLink({ data: { email, next } });
										setStatus('sent');
									} catch {
										setStatus('failed');
									}
								})();
							}}
						>
							<Field>
								<FieldLabel htmlFor="email">{t('auth.emailLabel')}</FieldLabel>
								<Input
									autoComplete="email"
									id="email"
									name="email"
									// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- React's own `ChangeEvent` type; not a declaration this file can edit.
									onChange={(event) => {
										setEmail(event.target.value);
									}}
									required
									type="email"
									value={email}
								/>
							</Field>
							<Button disabled={status === 'pending'} type="submit">
								{t('auth.sendLink')}
							</Button>
							{/* Announced, not merely rendered: a confirmation — or a failure — a
						    screen reader never reaches is the same as no feedback at all. */}
							<p aria-live="polite">
								{status === 'sent' ? t('auth.linkSent') : null}
								{status === 'failed' ? t('errors.unknown') : null}
							</p>
						</form>
					</CardContent>
				</Card>
			</div>
		</main>
	);
}

/**
 * The route's component: reads `next` from the URL and hands it to the form, which stays free of
 * the router so a story or a test can render it with a plain prop.
 *
 * @returns The sign-in form for this URL.
 */
function LoginPage(): React.JSX.Element {
	const { next } = useSearch({ from: '/login' });
	return <LoginForm next={next} />;
}

/**
 * Only a string can be a `next`. A search string can hold anything (TanStack parses numbers and
 * JSON literals out of it), and what is not a string is dropped rather than coerced: whether the
 * string is then followed is `isSafeNext`'s decision, on the server, not this parser's.
 *
 * @param search - The parsed search string.
 * @returns The search params this route understands.
 */
function validateLoginSearch(search: Readonly<Record<string, unknown>>): {
	readonly next?: string;
} {
	return { next: typeof search.next === 'string' ? search.next : undefined };
}

export const Route = createFileRoute('/login')({
	component: LoginPage,
	validateSearch: validateLoginSearch,
});
