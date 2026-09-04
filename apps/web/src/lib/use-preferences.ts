import { getRouteApi } from '@tanstack/react-router';

import type { Language, Theme } from './preferences';

const rootRoute = getRouteApi('__root__');

/**
 * The single seam between "how the root route carries the preferences down"
 * and "how a component reads them". Components call this and stay unaffected
 * if the transport changes from loader data to route context or back.
 */
export function usePreferences(): { language: Language; theme: Theme } {
	return rootRoute.useLoaderData();
}
