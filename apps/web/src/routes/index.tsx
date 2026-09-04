import { createFileRoute } from '@tanstack/react-router';
import { useTranslation } from 'react-i18next';

import { SiteFooter } from '../components/site-footer';
import { SiteHeader } from '../components/site-header';
import { usePreferences } from '../lib/use-preferences';

export const Route = createFileRoute('/')({
	component: Home,
});

function Home() {
	const { t } = useTranslation();
	const { theme } = usePreferences();

	return (
		<div className="bg-background text-foreground flex min-h-screen flex-col">
			<SiteHeader theme={theme} />
			<main className="flex flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
				<h1 className="text-3xl font-bold">{t('home.heading')}</h1>
				<p className="text-muted-foreground max-w-prose">{t('home.body')}</p>
				{/* Plan 6 replaces this with the real Supabase PKCE flow. It is a
				    visible, disabled control rather than a working-looking one, so
				    nobody mistakes it for an auth bug. */}
				<button
					className="bg-primary text-primary-foreground rounded px-4 py-2"
					disabled
					type="button"
				>
					{t('actions.signIn')}
				</button>
			</main>
			<SiteFooter />
		</div>
	);
}
