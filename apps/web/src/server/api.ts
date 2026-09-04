// `process` is ambient-typed here, in this file only, rather than via the root
// tsconfig's `types` field: `types` isn't additive — it would restrict
// automatic `@types/*` inclusion for the whole root program (which, per the
// root `include` globs, also covers packages/api-client/src and the root
// *.config.ts files) to solve a one-line need in this one file. This resolves
// through apps/web's own `@types/node` devDependency via TypeScript's
// per-file reference-directive resolution instead.
/// <reference types="node" />

import { createApiClient } from '@kurze-url/api-client';
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
		projectName: 'url-shortener-api',
		defaultHost: process.env.API_HOST ?? 'http://localhost:8080',
	});
}

// oxlint's typescript(explicit-function-return-type) is error-level and the
// brief's version omits this. `@kurze-url/api-client` doesn't export the
// generated `Client` type on its public surface (only `./generated`, not
// `./generated/client`, is re-exported), so `ReturnType<typeof createApiClient>`
// names the type without reaching past that boundary.
export function getApiClient(baseUrl: string = apiBaseUrl()): ReturnType<typeof createApiClient> {
	return createApiClient({ baseUrl });
}
