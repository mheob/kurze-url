import { describe, expect, it } from 'vitest';

import { classifyApiError, statusOf } from './api-errors';

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
	readonly value?: unknown;
}

interface FakeProblem {
	readonly detail?: string;
	readonly errors?: readonly FakeProblemDetail[];
	readonly status: number;
	readonly title: string;
}

function problem(
	status: number,
	errors?: readonly FakeProblemDetail[],
	detail?: string,
): FakeProblem {
	return { detail, errors, status, title: 'x' };
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

	it('maps a 409 with a blocking link count onto domainHasLinks', () => {
		// apps/api/internal/api/domains.go's deleteDomain attaches this exact
		// shape via `&huma.ErrorDetail{Location: "path.domain_id", Value:
		// linkCount}` alongside the free-text `detail` — the typed `value` is
		// what this reads, not any digit inside `detail`'s prose (that prose can
		// be reworded freely; see the "does not depend on the message wording"
		// test below).
		const failure = classifyApiError(
			problem(
				409,
				[{ location: 'path.domain_id', value: 3 }],
				'3 link(s) still use this domain; delete them first',
			),
		);
		expect(failure).toStrictEqual({ count: 3, kind: 'domainHasLinks' });
	});

	it('reads a singular blocking link count the same way', () => {
		const failure = classifyApiError(
			problem(
				409,
				[{ location: 'path.domain_id', value: 1 }],
				'1 link(s) still use this domain; delete them first',
			),
		);
		expect(failure).toStrictEqual({ count: 1, kind: 'domainHasLinks' });
	});

	it('does not depend on the message wording, only the typed value', () => {
		// Falsifies the coupling the prior, regex-based implementation had: the
		// same typed `errors` entry classifies the same way no matter how
		// `detail`'s prose is worded, reordered, or missing entirely.
		const failure = classifyApiError(
			problem(409, [{ location: 'path.domain_id', value: 3 }], 'only 3 more links to go'),
		);
		expect(failure).toStrictEqual({ count: 3, kind: 'domainHasLinks' });
	});

	it('falls back to unknown for a 409 with no typed value at all', () => {
		// The verify endpoint has its own, unrelated 409 — "another team has
		// already verified this hostname" — and attaches no ErrorDetail at all.
		// That must not be misread as a domainHasLinks failure with an invented
		// count of zero or one.
		const failure = classifyApiError(
			problem(409, undefined, 'another team has already verified this hostname'),
		);
		expect(failure).toStrictEqual({ kind: 'unknown' });
	});

	it('ignores an errors entry at an unrelated location', () => {
		const failure = classifyApiError(problem(409, [{ location: 'body.hostname', value: 3 }]));
		expect(failure).toStrictEqual({ kind: 'unknown' });
	});

	it('ignores a path.domain_id entry whose value is not a number', () => {
		const failure = classifyApiError(problem(409, [{ location: 'path.domain_id', value: '3' }]));
		expect(failure).toStrictEqual({ kind: 'unknown' });
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

describe('statusOf', () => {
	it('reads the numeric status off a thrown problem body', () => {
		expect(statusOf(problem(409))).toBe(409);
	});

	it('returns undefined for a non-object error', () => {
		expect(statusOf(new Error('network'))).toBeUndefined();
	});

	it('returns undefined when status is missing or not a number', () => {
		expect(statusOf({ status: '409' })).toBeUndefined();
		expect(statusOf({})).toBeUndefined();
	});
});

describe('a slug conflict', () => {
	it('is its own kind, not the generic unknown failure', () => {
		expect(
			classifyApiError(
				problem(
					409,
					[{ location: 'body.slug', message: 'this slug is already taken' }],
					'a team with that slug already exists',
				),
			),
		).toEqual({ kind: 'slugTaken' });
	});

	/**
	 * Keyed on the typed location, never on the prose: a 409 that carries no
	 * recognised detail — the domain verify endpoint's "another team already
	 * verified this hostname" — must keep collapsing into `unknown`, or every
	 * such call site would start rendering a message about slugs.
	 */
	it('does not swallow a conflict that carries no field detail', () => {
		expect(classifyApiError(problem(409, undefined, 'already verified elsewhere'))).toEqual({
			kind: 'unknown',
		});
	});

	/**
	 * Falsifies message-text matching directly: this 409's prose mentions
	 * "slug", but the typed `location` is `body.hostname`, not `body.slug`. An
	 * implementation that classified on `detail`'s wording instead of the typed
	 * location would misfile this as `slugTaken` — it must stay `unknown`.
	 */
	it('does not classify on message wording when the location is unrelated', () => {
		expect(
			classifyApiError(
				problem(
					409,
					[{ location: 'body.hostname', message: 'hostname already claimed' }],
					'the requested slug conflicts with an existing team',
				),
			),
		).toEqual({ kind: 'unknown' });
	});
});
