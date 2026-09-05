import { type Client, createClient, createConfig } from './generated/client';
import type { ClientOptions as GeneratedClientOptions } from './generated/types.gen';

/**
 * How the client obtains a bearer token for each request.
 *
 * It is a function rather than a string because a Supabase access token
 * expires: reading it per request lets the caller hand back whatever the
 * session currently holds, including one refreshed since the client was built.
 * Returning `undefined` sends the request unauthenticated.
 *
 * The token is only ever attached to operations whose OpenAPI definition
 * declares `bearerAuth`, so `getHealth` stays anonymous without special-casing.
 */
type GetAccessToken = () => string | undefined | Promise<string | undefined>;

interface ApiClientOptions {
	/** Origin the API answers on, e.g. `https://api.kurze.url`. No trailing path. */
	readonly baseUrl: string;
	/** Omit for an unauthenticated client — only `/v1/health` is reachable then. */
	readonly getAccessToken?: GetAccessToken;
	/** Swapped in tests. Defaults to the global `fetch`. */
	readonly fetch?: typeof globalThis.fetch;
	/**
	 * Sent on every request this client makes. For headers the caller's
	 * platform requires rather than the API itself — the API's own auth goes
	 * through `getAccessToken`, so nothing here should carry a bearer token.
	 */
	readonly headers?: Readonly<Record<string, string>>;
}

/**
 * Builds a client for the kurze-url API.
 *
 * Every path, parameter, request body and response is generated from
 * `apps/api/openapi.json`, which is itself generated from the Go handlers — so
 * an endpoint that changes shape breaks compilation here rather than at runtime
 * in a browser.
 *
 * Pass the result to the generated operations, which each accept a `client`:
 *
 * ```ts
 * const client = createApiClient({ baseUrl, getAccessToken });
 * const { data } = await listLinks({ client, path: { team_id: teamId } });
 * ```
 *
 * A fresh instance is returned rather than configuring the module-level client
 * the generator emits, because the frontend renders on the server: one process
 * serves many people there, and a shared client would leak one person's token
 * into another's request.
 *
 * The redirect surface (`GET /{slug}`) is deliberately absent. It is not in the
 * OpenAPI document, it answers on the short-link hostnames rather than the
 * API's own, and nothing should reach it through a generated client.
 *
 * @param options Base URL, an optional token supplier, optional per-request
 *   headers, and an optional `fetch`.
 * @returns A client to hand to the generated operations.
 */
function createApiClient(options: ApiClientOptions): Client {
	return createClient(
		createConfig<GeneratedClientOptions>({
			auth: options.getAccessToken,
			baseUrl: options.baseUrl,
			fetch: options.fetch,
			headers: options.headers,
		}),
	);
}

export { createApiClient };
export type { ApiClientOptions, GetAccessToken };
export * from './generated';
