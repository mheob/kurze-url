import { TanStackDevtools } from '@tanstack/react-devtools';
import { HeadContent, Scripts, createRootRoute } from '@tanstack/react-router';
import { TanStackRouterDevtoolsPanel } from '@tanstack/react-router-devtools';
import { createServerFn } from '@tanstack/react-start';
import { getRequestHeader } from '@tanstack/react-start/server';
import { useMemo } from 'react';
import { I18nextProvider } from 'react-i18next';

import { createI18n } from '../i18n';
import { readLanguage, readTheme } from '../lib/preferences';

import appCss from '../styles/app.css?url';

const devtoolsConfig = {
	position: 'bottom-right',
} as const;

const devtoolsPlugins = [
	{
		name: 'Tanstack Router',
		render: <TanStackRouterDevtoolsPanel />,
	},
];

/**
 * Loaders are isomorphic — they also run on the client — but reading the
 * request's cookies is not: `getRequestHeader` only works inside the
 * server's per-request AsyncLocalStorage context and throws elsewhere.
 * Wrapping the read in a server function keeps the call isomorphic-safe:
 * during the initial SSR pass it runs in-process (no network round trip, so
 * the value lands in the same response as the HTML), and on the client it
 * becomes a regular RPC instead of a crash.
 */
const getPreferences = createServerFn({ method: 'GET' }).handler(async () => {
	const cookieHeader = getRequestHeader('cookie');
	return {
		language: readLanguage(cookieHeader),
		theme: readTheme(cookieHeader),
	};
});

function RootDocument({ children }: { readonly children: React.ReactNode }) {
	const { language, theme } = Route.useLoaderData();
	// A fresh i18n instance per request already (see createI18n's own docstring);
	// memoizing here just stops the client from rebuilding it on every
	// unrelated re-render — it only changes if the language itself does.
	const i18n = useMemo(() => createI18n(language), [language]);

	return (
		<html className={theme === 'dark' ? 'dark' : undefined} lang={language}>
			<head>
				<HeadContent />
			</head>
			<body className="bg-background text-foreground">
				<I18nextProvider i18n={i18n}>{children}</I18nextProvider>
				<TanStackDevtools config={devtoolsConfig} plugins={devtoolsPlugins} />
				<Scripts />
			</body>
		</html>
	);
}

export const Route = createRootRoute({
	loader: () => getPreferences(),
	head: () => ({
		links: [
			{
				href: appCss,
				rel: 'stylesheet',
			},
		],
		meta: [
			{
				charSet: 'utf8',
			},
			{
				content: 'width=device-width, initial-scale=1',
				name: 'viewport',
			},
			// Deliberately no `title` entry: @tanstack/react-router only emits a
			// <title> tag when `meta.title` is truthy, and this app ships zero
			// hardcoded user-facing strings. Leave this page title-less until the
			// i18n task can supply a translated one — don't "helpfully" restore it.
		],
	}),
	shellComponent: RootDocument,
});
