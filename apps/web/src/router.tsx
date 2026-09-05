import { QueryClient } from '@tanstack/react-query';
import { createRouter as createTanStackRouter } from '@tanstack/react-router';
import { setupRouterSsrQueryIntegration } from '@tanstack/react-router-ssr-query';

import { routeTree } from './routeTree.gen';

/**
 * The plan's own sample named `@tanstack/react-router-with-query` here —
 * that package has since been superseded by `@tanstack/react-router-ssr-query`
 * (confirmed via TanStack's own current router-query integration docs and
 * skill file, `github.com/tanstack/router`'s
 * `docs/router/integrations/query.md` and
 * `packages/react-router/skills/compositions/router-query/SKILL.md`); it
 * was not a dependency of this repo before this task and is added by it. See
 * task-9-report.md for the divergence this note refers to.
 *
 * `setupRouterSsrQueryIntegration` does three things at once:
 * - wires `context.queryClient` into every route, which is what
 *   `teams.$teamId.links.index.tsx`'s loader calls `ensureQueryData` on
 * - dehydrates whatever queries were populated during the server render into
 *   the SSR payload, and rehydrates them into the client's own (separate)
 *   `QueryClient` — without this, a client-side run of the same loader
 *   would find an empty cache and refetch data the server already sent down
 * - wraps the router in a `QueryClientProvider` (`wrapQueryClient` defaults
 *   to `true`), so `useSuspenseQuery` is reachable from route components
 *   with no change to `__root.tsx`'s own shell
 *
 * The `QueryClient` is created fresh inside this function, never at module
 * scope: this app is server-rendered, one process serves many people there,
 * and a module-level instance would let one visitor's cached links leak into
 * another's response — the exact hazard `packages/api-client/src/index.ts`'s
 * `createApiClient` docstring describes for why it returns a fresh client
 * per call rather than exporting a shared one.
 */
export function getRouter() {
	const queryClient = new QueryClient();

	const router = createTanStackRouter({
		context: { queryClient },
		defaultPreload: 'intent',
		defaultPreloadStaleTime: 0,
		routeTree,
		scrollRestoration: true,
	});

	setupRouterSsrQueryIntegration({ queryClient, router });

	return router;
}

declare module '@tanstack/react-router' {
	interface Register {
		router: ReturnType<typeof getRouter>;
	}
}
