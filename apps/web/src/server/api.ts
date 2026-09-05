// `process` is ambient-typed here, in this file only, rather than via the root
// tsconfig's `types` field: `types` isn't additive — it would restrict
// automatic `@types/*` inclusion for the whole root program (which, per the
// root `include` globs, also covers packages/api-client/src and the root
// *.config.ts files) to solve a one-line need in this one file. This resolves
// through apps/web's own `@types/node` devDependency via TypeScript's
// per-file reference-directive resolution instead.
/// <reference types="node" />

import { createApiClient, type GetAccessToken } from '@kurze-url/api-client';
import { withRelatedProject } from '@vercel/related-projects';

/**
 * The only place createApiClient is called. The browser never reaches the Go
 * API: every call runs here, on the server, which is what removes the CORS
 * question entirely and lets Plan 6 keep the access token in an httpOnly
 * cookie the browser cannot read.
 *
 * withRelatedProject resolves the API URL per deployment, so a preview of this
 * app talks to the matching preview of the API rather than to production.
 */
export function apiBaseUrl(): string {
	return withRelatedProject({
		projectName: 'kurze-url-api',
		defaultHost: process.env.API_HOST ?? 'http://localhost:8080',
	});
}

/**
 * Headers this deployment has to send for the API to answer it at all, as
 * opposed to anything the API's own contract asks for.
 *
 * A preview of the API sits behind Vercel Authentication, which answers an
 * unauthenticated request with a 302 to vercel.com/sso-api rather than an
 * error. This call is server-to-server, so there is no browser to complete
 * that login: the probe followed the redirect, parsed Vercel's HTML as JSON,
 * failed, and every preview of this app reported the API unreachable while the
 * API was in fact healthy.
 *
 * API_PROTECTION_BYPASS_SECRET is the *API* project's Protection Bypass for
 * Automation secret, set on this project's Preview environment only. Scoping
 * it by environment is what keeps the branch out of the code: production
 * deployments have no secret to send because production is public, and there
 * is nothing to forget in a conditional.
 *
 * Deliberately not named VERCEL_AUTOMATION_BYPASS_SECRET: Vercel injects that
 * name into this project's own deployments with this project's own secret,
 * which unlocks the web app, not the API.
 */
function platformHeaders(): Record<string, string> {
	const bypass = process.env.API_PROTECTION_BYPASS_SECRET;
	return bypass ? { 'x-vercel-protection-bypass': bypass } : {};
}

// oxlint's typescript(explicit-function-return-type) is error-level and the
// brief's version omits this. `@kurze-url/api-client` doesn't export the
// generated `Client` type on its public surface (only `./generated`, not
// `./generated/client`, is re-exported), so `ReturnType<typeof createApiClient>`
// names the type without reaching past that boundary.
//
// `getAccessToken` is optional so the existing anonymous, single-argument
// callers (e.g. the health check) keep working unchanged: an authenticated
// caller passes a supplier, everyone else gets a client where only
// `bearerAuth`-free operations are reachable.
export function getApiClient(
	baseUrl: string = apiBaseUrl(),
	getAccessToken?: GetAccessToken,
): ReturnType<typeof createApiClient> {
	return createApiClient({ baseUrl, getAccessToken, headers: platformHeaders() });
}
