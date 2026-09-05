/**
 * Turns whatever a failed API call throws into something a route or form can
 * act on, without either of them needing to know Huma's wire format.
 *
 * The shape this inspects is grounded in `apps/api/openapi.json`'s
 * `ErrorModel`/`ErrorDetail` schemas and in how
 * `packages/api-client/src/generated/client/client.gen.ts` actually surfaces
 * a failure: every operation's error response is `application/problem+json`
 * (RFC 9457), and with `throwOnError: true` — the convention already
 * established in `src/server/health.ts` and `src/routes/_authed.tsx` — the
 * generated client throws that parsed JSON body directly. There is no
 * `.response`/`.error` wrapper around it; `status` and `errors` sit at the
 * top level. See api-errors.test.ts for the divergence from an earlier,
 * unverified assumption about this shape.
 */
export type ApiFailure =
	| { kind: 'unauthenticated' }
	| { kind: 'notFound' }
	| { kind: 'rateLimited' }
	| { kind: 'fields'; fields: Record<string, string> }
	| { kind: 'unknown' };

/** The one `ErrorDetail` field this module reads; see `apps/api/openapi.json`. */
interface ProblemDetail {
	readonly location?: string;
	readonly message?: string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === 'object' && value !== null;
}

function isProblemDetail(value: unknown): value is ProblemDetail {
	if (!isRecord(value)) return false;
	const { location, message } = value;
	return (
		(location === undefined || typeof location === 'string') &&
		(message === undefined || typeof message === 'string')
	);
}

function statusOf(error: unknown): number | undefined {
	if (!isRecord(error)) return undefined;
	const { status } = error;
	return typeof status === 'number' ? status : undefined;
}

function problemDetailsOf(error: unknown): readonly ProblemDetail[] {
	if (!isRecord(error)) return [];
	const { errors } = error;
	if (!Array.isArray(errors) || !errors.every(isProblemDetail)) return [];
	return errors;
}

/**
 * Huma's `location` is prefixed by where the value came from — `body`,
 * `query`, `path`, or `header` — e.g. `body.destination_url` or
 * `path.thing-id` (see `huma.ErrorDetail`'s doc comment). A bare prefix with
 * nothing after it has no field to attach to: Huma emits exactly `"body"`,
 * with no dot, for a request body that failed to parse as JSON at all
 * (`validateBody` in `huma.go`) — before any field-level validation ran. That
 * is a whole-request problem, not a report about a field named "body", and
 * must not be mistaken for one.
 */
function fieldNameOf(location: string | undefined): string | undefined {
	if (!location?.includes('.')) return undefined;
	return location.split('.').pop();
}

function fieldsOf(error: unknown): Record<string, string> {
	const fields: Record<string, string> = {};

	for (const detail of problemDetailsOf(error)) {
		const name = fieldNameOf(detail.location);
		if (name && detail.message) fields[name] = detail.message;
	}

	return fields;
}

export function classifyApiError(error: unknown): ApiFailure {
	const status = statusOf(error);

	if (status === 401) return { kind: 'unauthenticated' };
	// The API answers 404 for a non-member so it never confirms a team
	// exists; treating 403 differently from 404 here would leak exactly what
	// internal/authz withholds.
	if (status === 403 || status === 404) return { kind: 'notFound' };
	if (status === 429) return { kind: 'rateLimited' };

	if (status === 400 || status === 422) {
		const fields = fieldsOf(error);
		if (Object.keys(fields).length > 0) return { fields, kind: 'fields' };
	}

	return { kind: 'unknown' };
}
