import { describe, expect, it } from 'vitest';

import { classifyApiError } from './api-errors';

/**
 * Shapes copied from what Huma/the generated client actually produce, not
 * from the task brief's `problem()` helper.
 *
 * `apps/api/openapi.json` declares every operation's error response as a
 * single `default: ErrorModel` (RFC 9457 `application/problem+json`) — there
 * is no per-status-code schema to diverge from. The generated client
 * (`packages/api-client/src/generated/client/client.gen.ts`, `throw
 * jsonError ?? textError`) throws that parsed body directly when
 * `throwOnError: true` — the convention already used in
 * `src/server/health.ts` and `src/routes/_authed.tsx` — with no
 * `.response`/`.error` wrapper around it. `status` and `errors` sit at the
 * top level of the thrown value itself.
 */
interface FakeProblemDetail {
	readonly location?: string;
	readonly message?: string;
}

interface FakeProblem {
	readonly errors?: readonly FakeProblemDetail[];
	readonly status: number;
	readonly title: string;
}

function problem(status: number, errors?: readonly FakeProblemDetail[]): FakeProblem {
	return { errors, status, title: 'x' };
}

describe('classifyApiError', () => {
	it('maps 401 to unauthenticated', () => {
		expect(classifyApiError(problem(401))).toStrictEqual({ kind: 'unauthenticated' });
	});

	it('maps 403 to notFound, not to a forbidden state', () => {
		// The API answers 404 for a non-member, but a link inside a team you
		// were just removed from can still return 403. Both must render the
		// same thing, or the UI reintroduces the disclosure authz avoids.
		expect(classifyApiError(problem(403))).toStrictEqual({ kind: 'notFound' });
		expect(classifyApiError(problem(404))).toStrictEqual({ kind: 'notFound' });
	});

	it('maps 429 to rateLimited', () => {
		expect(classifyApiError(problem(429))).toStrictEqual({ kind: 'rateLimited' });
	});

	it('maps 422 field errors onto field names', () => {
		const failure = classifyApiError(
			problem(422, [{ location: 'body.destination_url', message: 'must be a valid URL' }]),
		);
		expect(failure).toStrictEqual({
			fields: { destination_url: 'must be a valid URL' },
			kind: 'fields',
		});
	});

	it('collects more than one field error', () => {
		const failure = classifyApiError(
			problem(422, [
				{ location: 'body.destination_url', message: 'must be a valid URL' },
				{ location: 'body.slug', message: 'already taken' },
			]),
		);
		expect(failure).toStrictEqual({
			fields: { destination_url: 'must be a valid URL', slug: 'already taken' },
			kind: 'fields',
		});
	});

	it('does not mistake a bare "body" location for a field named body', () => {
		// Huma's validateBody emits exactly this for a request body that failed
		// to parse as JSON at all — location "body", no dot, no field-level
		// validation ever ran. A naive `location.split('.').pop()` (the task
		// brief's version) would read that as a field literally named "body",
		// silently misfiling a whole-request problem as a per-field one that no
		// form input is ever named after. With no field to attach to and no
		// form-level slot in ApiFailure, this must fall through to `unknown`.
		const failure = classifyApiError(
			problem(400, [{ location: 'body', message: 'invalid character } looking for value' }]),
		);
		expect(failure).toStrictEqual({ kind: 'unknown' });
	});

	it('treats a 400 with a proper field location the same as a 422', () => {
		const failure = classifyApiError(
			problem(400, [{ location: 'body.destination_url', message: 'required' }]),
		);
		expect(failure).toStrictEqual({ fields: { destination_url: 'required' }, kind: 'fields' });
	});

	it('ignores a field error with no message', () => {
		const failure = classifyApiError(problem(422, [{ location: 'body.slug' }]));
		expect(failure).toStrictEqual({ kind: 'unknown' });
	});

	it('handles a null errors array (ErrorModel.errors is nullable)', () => {
		expect(classifyApiError({ errors: null, status: 422 })).toStrictEqual({ kind: 'unknown' });
	});

	it('falls back to unknown for anything else', () => {
		expect(classifyApiError(new Error('network'))).toStrictEqual({ kind: 'unknown' });
	});

	it('falls back to unknown for a non-object error', () => {
		expect(classifyApiError('fetch failed')).toStrictEqual({ kind: 'unknown' });
	});
});
