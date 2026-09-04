import { TanStackDevtools } from '@tanstack/react-devtools';
import { HeadContent, Scripts, createRootRoute } from '@tanstack/react-router';
import { TanStackRouterDevtoolsPanel } from '@tanstack/react-router-devtools';
import { createServerFn } from '@tanstack/react-start';
import { getRequestHeader } from '@tanstack/react-start/server';
import { useMemo } from 'react';
import { I18nextProvider, useTranslation } from 'react-i18next';

import { createI18n, documentTitle } from '../i18n';
import { DEFAULT_LANGUAGE, readLanguage, readTheme, themeClassName } from '../lib/preferences';

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
	// Only ever the initial guess before a `lang` cookie exists — `readLanguage`
	// reads this exclusively as a fallback for a request with no cookie at all.
	const acceptLanguageHeader = getRequestHeader('accept-language');
	return {
		language: readLanguage(cookieHeader, acceptLanguageHeader),
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
		<html className={themeClassName(theme)} lang={language}>
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

/**
 * TanStack Router's own default `notFoundComponent` is a hardcoded English
 * literal ("Not Found") with no translation hook at all — it renders for any
 * URL that matches no route, regardless of the request's language, so an
 * unrecognised `/de/...`-flavoured link would otherwise ship English text
 * inside an already-correctly-German `<html lang="de">` shell.
 */
function NotFound() {
	const { t } = useTranslation();

	return (
		<main className="flex flex-1 flex-col items-center justify-center gap-4 px-6 text-center">
			<h1 className="text-3xl font-bold">{t('notFound.heading')}</h1>
			<p className="text-muted-foreground max-w-prose">{t('notFound.body')}</p>
		</main>
	);
}

export const Route = createRootRoute({
	loader: () => getPreferences(),
	notFoundComponent: NotFound,
	// `loaderData` is what makes `head` able to see the request's language at
	// all — it runs before `RootDocument` (and its `I18nextProvider`) exists,
	// so `documentTitle` reads the catalogue directly instead of going through
	// `useTranslation`. Axe's `document-title` check (WCAG 2.4.2) needs this
	// non-empty, and it must still be translated like everything else here.
	head: ({ loaderData }) => ({
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
			{
				title: documentTitle(loaderData?.language ?? DEFAULT_LANGUAGE),
			},
		],
	}),
	shellComponent: RootDocument,
});
