/// <reference types="vite/client" />

/**
 * Vite's own `ImportMetaEnv` carries an index signature that resolves every
 * key it does not know to `any`, so `import.meta.env.VITE_SENTRY_DSN` typed as
 * `any` and spread that `any` through everything it touched — four
 * `no-unsafe-*` findings in `lib/observability.ts` alone, one of them an `any`
 * arriving where a `string` was declared.
 *
 * Declaring the three keys this app reads restores the checking. They are
 * `string | undefined` rather than `string` because none is required: an unset
 * DSN disables Sentry reporting instead of erroring, and the other two fall
 * back to a literal at the point of use.
 *
 * Adding a `VITE_` variable means adding it here too, or it silently returns
 * to being `any`.
 */
interface ImportMetaEnv {
	/** Sentry DSN for the browser bundle. Empty or unset disables reporting. */
	readonly VITE_SENTRY_DSN?: string;
	/** Deployment environment tag. Defaults to `development` where unset. */
	readonly VITE_SENTRY_ENVIRONMENT?: string;
	/** Release identifier, normally the commit sha. Unset leaves it off the event. */
	readonly VITE_SENTRY_RELEASE?: string;
}

interface ImportMeta {
	readonly env: ImportMetaEnv;
}
