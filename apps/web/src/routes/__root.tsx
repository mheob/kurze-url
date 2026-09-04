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
			{
				title: 'TanStack Start Starter',
			},
		],
	}),
	shellComponent: RootDocument,
});
