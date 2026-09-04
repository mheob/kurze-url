import { getHealth } from '@kurze-url/api-client';

import { apiBaseUrl, getApiClient } from './api';

// oxlint's typescript(consistent-type-definitions) rule is error-level here and
// rejects a `type` alias for an object shape — `interface` only, per the brief's
// own deviation note.
export interface HealthStatus {
	status: string;
}

/**
 * Degrades rather than throws. This probe exists to prove the deployment
 * wiring, and a shell that fails to render because the API is down would
 * report the wrong problem.
 */
export async function fetchHealth(baseUrl: string = apiBaseUrl()): Promise<HealthStatus> {
	try {
		// throwOnError is required here: the generated client's default
		// (false) never rejects — a network failure resolves to
		// `{ data: undefined, error }` instead of throwing, so the catch
		// below would never run and a downed API would report 'unknown'
		// rather than 'unreachable'.
		const { data } = await getHealth({ client: getApiClient(baseUrl), throwOnError: true });
		return { status: data?.status ?? 'unknown' };
	} catch {
		return { status: 'unreachable' };
	}
}
