import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '../components/ui/button';
import { sendMagicLink } from '../server/auth';

// A fourth `'failed'` status, not a second boolean alongside `sent`: this
// form has never had more than one outcome on screen at once, and a status
// value is what keeps that true by construction — `sent` and `failed` both
// `true` at once was a reachable, meaningless state a second boolean would
// have allowed.
type SendStatus = 'failed' | 'idle' | 'pending' | 'sent';

export function LoginForm(): React.JSX.Element {
	const { t } = useTranslation();
	const [status, setStatus] = useState<SendStatus>('idle');
	const [email, setEmail] = useState('');

	return (
		<form
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
				void sendMagicLink({ data: { email } })
					.then(() => setStatus('sent'))
					.catch(() => setStatus('failed'));
			}}
		>
			<h1>{t('auth.signInTitle')}</h1>
			<label htmlFor="email">{t('auth.emailLabel')}</label>
			<input
				autoComplete="email"
				id="email"
				name="email"
				onChange={(event) => setEmail(event.target.value)}
				required
				type="email"
				value={email}
			/>
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
	);
}

export const Route = createFileRoute('/login')({ component: LoginForm });
