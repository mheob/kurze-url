import { createFileRoute } from '@tanstack/react-router';
import { useState } from 'react';
import { useTranslation } from 'react-i18next';

import { Button } from '../components/ui/button';
import { sendMagicLink } from '../server/auth';

export function LoginForm(): React.JSX.Element {
	const { t } = useTranslation();
	const [sent, setSent] = useState(false);
	const [email, setEmail] = useState('');

	return (
		<form
			onSubmit={(event) => {
				event.preventDefault();
				void sendMagicLink({ data: { email } }).then(() => setSent(true));
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
			<Button type="submit">{t('auth.sendLink')}</Button>
			{/* Announced, not merely rendered: a confirmation a screen reader
			    never reaches is the same as no confirmation. */}
			<p aria-live="polite">{sent ? t('auth.linkSent') : ''}</p>
		</form>
	);
}

export const Route = createFileRoute('/login')({ component: LoginForm });
