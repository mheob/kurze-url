import createOpenApiClient, {
	type Client,
	type ClientOptions,
	type Middleware,
} from 'openapi-fetch';

import type { paths } from './schema.js';

/**
 * How the client obtains a bearer token for each request.
 *
 * It is a function rather than a string because a Supabase access token
 * expires: reading it per request lets the caller hand back whatever the
 * session currently holds, including one refreshed since the client was built.
 * Returning `undefined` sends the request unauthenticated, which is what the
 * public operations expect.
 */
type GetAccessToken = () => string | undefined | Promise<string | undefined>;

interface ApiClientOptions {
	/** Origin the API answers on, e.g. `https://api.kurze.url`. No trailing path. */
	readonly baseUrl: string;
	/** Omit for an unauthenticated client — only `/v1/health` is reachable then. */
	readonly getAccessToken?: GetAccessToken;
	/** Swapped in tests. Defaults to the global `fetch`. */
	readonly fetch?: ClientOptions['fetch'];
}

/**
 * Attaches the bearer token, resolved per request rather than per client.
 *
 * @param getAccessToken Supplies the current token, or `undefined` to send the
 *   request unauthenticated.
 * @returns Middleware that sets `Authorization` when a token is available.
 */
function bearerAuth(getAccessToken: GetAccessToken): Middleware {
	return {
		// oxlint-disable-next-line typescript/prefer-readonly-parameter-types
		async onRequest({ request }: { request: Request }) {
			const token = await getAccessToken();

			// An empty string counts as no token. Sending `Bearer ` would be
			// rejected as malformed rather than as unauthenticated, which is a
			// worse error for the caller to debug.
			if (token !== undefined && token !== '') {
				request.headers.set('Authorization', `Bearer ${token}`);
			}

			return request;
		},
	};
}

/**
 * Builds a typed client for the kurze-url API.
 *
 * Every path, parameter, request body and response is checked against
 * `apps/api/openapi.json`, which is generated from the Go handlers themselves —
 * so an endpoint that changes shape breaks compilation here rather than at
 * runtime in a browser.
 *
 * The redirect surface (`GET /{slug}`) is deliberately absent: it is not part
 * of the OpenAPI document, it answers on the short-link hostnames rather than
 * the API's own, and nothing should reach it through a generated client.
 *
 * @param options Base URL, an optional token supplier, and an optional `fetch`.
 * @returns A client whose methods mirror the API's paths.
 */
function createApiClient(options: ApiClientOptions): Client<paths> {
	const client = createOpenApiClient<paths>({
		baseUrl: options.baseUrl,
		fetch: options.fetch,
	});

	if (options.getAccessToken) {
		client.use(bearerAuth(options.getAccessToken));
	}

	return client;
}

export { createApiClient };
export type { ApiClientOptions, GetAccessToken };
export type { components, operations, paths } from './schema.js';
