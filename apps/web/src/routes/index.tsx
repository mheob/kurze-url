import { createFileRoute } from '@tanstack/react-router';
import { createServerFn } from '@tanstack/react-start';
import { useTranslation } from 'react-i18next';

import { SiteFooter } from '../components/site-footer';
import { SiteHeader } from '../components/site-header';
import { Button } from '../components/ui/button';
import { usePreferences } from '../lib/use-preferences';
import { fetchHealth } from '../server/health';

/**
 * Wraps fetchHealth in a server function so the loader — isomorphic, like
 * __root.tsx's getPreferences — never runs the API call in the browser. On
 * the initial SSR pass this executes in-process; on a client-side navigation
 * back to this route it becomes an RPC to this app's own server, which is the
 * only thing the browser ever talks to.
 */
const getHealthStatus = createServerFn({ method: 'GET' }).handler(() => fetchHealth());

export const Route = createFileRoute('/')({
	component: Home,
	loader: () => getHealthStatus(),
});

function Home() {
	const { t } = useTranslation();
	const { theme } = usePreferences();
	const { status } = Route.useLoaderData();

	return (
		<div className="bg-background text-foreground flex min-h-screen flex-col">
			<SiteHeader theme={theme} />
			<main className="flex flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
				<h1 className="text-3xl font-bold">{t('home.heading')}</h1>
				<p className="text-muted-foreground max-w-prose">{t('home.body')}</p>
				{/* Plan 6 replaces this with the real Supabase PKCE flow. It is a
				    visible, disabled control rather than a working-looking one, so
				    nobody mistakes it for an auth bug. */}
				<Button disabled type="button">
					{t('actions.signIn')}
				</Button>
			</main>
			<SiteFooter apiStatus={status} />
		</div>
	);
}
