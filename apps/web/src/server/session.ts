import { getApiClient } from './api';
import { createSupabase } from './supabase';

/**
 * Distinguishable from an API 401 on purpose. This one means "no session on
 * this request" and the answer is a redirect to /login; an API 401 means the
 * token was rejected, and conflating them turns a normal signed-out visit into
 * a round trip to the Go service.
 */
export class UnauthenticatedError extends Error {
	constructor() {
		super('no session');
		this.name = 'UnauthenticatedError';
	}
}

/**
 * Reading the session is also what refreshes it: @supabase/ssr renews an
 * expired one and writes the new cookies through the adapter's setAll. That is
 * why `headers` is threaded all the way down here rather than only used at
 * sign-in.
 */
export async function getAccessToken(
	request: Request,
	headers: Headers,
): Promise<string | undefined> {
	const supabase = createSupabase(request, headers);
	const { data } = await supabase.auth.getSession();
	return data.session?.access_token;
}

/**
 * Fails closed: an empty or missing token throws rather than falling through
 * to an "authenticated" request the API will 401 anyway. That 401 would be the
 * same symptom, three layers further from the cause.
 */
export async function requireSession(
	request: Request,
	headers: Headers,
): Promise<{ accessToken: string }> {
	const accessToken = await getAccessToken(request, headers);
	if (!accessToken) throw new UnauthenticatedError();
	return { accessToken };
}

/**
 * The one place callers turn a known-good token into an API client. Every
 * authenticated server function calls `requireSession` then this.
 */
export function authedApiClient(accessToken: string): ReturnType<typeof getApiClient> {
	return getApiClient(undefined, () => accessToken);
}
