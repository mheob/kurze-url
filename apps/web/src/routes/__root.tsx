import { TanStackDevtools } from '@tanstack/react-devtools';
import { HeadContent, Scripts, createRootRoute } from '@tanstack/react-router';
import { TanStackRouterDevtoolsPanel } from '@tanstack/react-router-devtools';

import appCss from '../styles.css?url';

const devtoolsConfig = {
	position: 'bottom-right',
} as const;

const devtoolsPlugins = [
	{
		name: 'Tanstack Router',
		render: <TanStackRouterDevtoolsPanel />,
	},
];

function RootDocument({ children }: { readonly children: React.ReactNode }) {
	return (
		<html lang="en">
			<head>
				<HeadContent />
			</head>
			<body>
				{children}
				<TanStackDevtools config={devtoolsConfig} plugins={devtoolsPlugins} />
				<Scripts />
			</body>
		</html>
	);
}

export const Route = createRootRoute({
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
