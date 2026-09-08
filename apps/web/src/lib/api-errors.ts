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
 * `.response`/`.error` wrapper around it; `status`, `errors`, and `detail`
 * all sit at the top level. See api-errors.test.ts for the divergence from
 * an earlier, unverified assumption about this shape.
 */
export type ApiFailure =
	| { kind: 'unauthenticated' }
	| { kind: 'notFound' }
	| { kind: 'rateLimited' }
	| { kind: 'fields'; fields: Record<string, string> }
	| { kind: 'domainHasLinks'; count: number }
	| { kind: 'slugTaken' }
	| { kind: 'unknown' };

/** The `ErrorDetail` fields this module reads; see `apps/api/openapi.json`. */
interface ProblemDetail {
	readonly location?: string;
	readonly message?: string;
	readonly value?: unknown;
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

/**
 * Exported for the rare call site that needs the raw HTTP status alongside
 * `ApiFailure`'s kind — `teams.$teamSlug.domains.tsx`'s verify mutation is the
 * first: a 409 there ("another team already verified this hostname") carries
 * no `ErrorDetail` to key on, the same as a 500 or a network failure, so
 * `classifyApiError` alone cannot tell them apart — both fall into `unknown`.
 * That collapse is correct for every other caller (nothing else needs to
 * split them), so this stays a plain status accessor rather than a new
 * `ApiFailure` kind that would force every other 409-without-detail call site
 * (members, tags, link slugs) to adopt a message that does not fit them.
 */
export function statusOf(error: unknown): number | undefined {
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

/**
 * `deleteDomain` in apps/api/internal/api/domains.go is the only place a 409
 * carries a link count, and it sends it as a typed `ErrorDetail` alongside
 * the free-text `detail`: `Location: "path.domain_id", Value: linkCount` —
 * see the comment on that call for why that `Location` string was chosen.
 * Reading the typed value here, rather than parsing it out of `detail`'s
 * prose, means a reworded message can never silently break this: if the
 * typed detail ever stops arriving, this returns `undefined`, exactly like a
 * 409 that never carried one — there is deliberately no regex fallback onto
 * `detail`, since that would let this exact drift happen quietly again.
 *
 * The *verify* endpoint's own unrelated conflict ("another team has already
 * verified this hostname") carries no `ErrorDetail` at all, so it falls
 * through to `undefined` here too, never an invented count.
 */
function blockingLinkCountOf(error: unknown): number | undefined {
	for (const detail of problemDetailsOf(error)) {
		if (detail.location === 'path.domain_id' && typeof detail.value === 'number') {
			return detail.value;
		}
	}
	return undefined;
}

/**
 * `createTeam` answers a taken slug with 409 and a typed detail on the field,
 * the same convention `deleteDomain`'s blocking-link count uses one level over
 * (`path.domain_id` there, a body field here). Reading `location` rather than
 * matching the message means a reworded message cannot silently turn this back
 * into a generic failure — and there is deliberately no text fallback, since
 * that is exactly how such drift goes unnoticed.
 *
 * This matches any 409 carrying `location === 'body.slug'`, not only one from
 * `createTeam` — it is just the only caller today. The link-slug conflicts in
 * `apps/api/internal/api/links.go` (`create link`, `update link`) still
 * answer with a free-text 409 and no `ErrorDetail`, so they never reach this
 * function. The moment one of those gains a typed detail here, this function
 * starts matching it too, and `classifyApiError` reports `slugTaken` for a
 * link the same way it does for a team — but the create-link and edit-link
 * banners render `t(\`errors.${failure.kind}\`)` for every kind except
 * `fields`, and `errors.slugTaken` exists in neither catalogue. Whoever adds
 * that typed detail to a link-slug 409 needs to also teach those two banners
 * about `slugTaken`, the way `new-team.tsx` already excludes it from its own.
 */
function isSlugConflict(error: unknown): boolean {
	return problemDetailsOf(error).some((detail) => detail.location === 'body.slug');
}

export function classifyApiError(error: unknown): ApiFailure {
	const status = statusOf(error);

	if (status === 401) return { kind: 'unauthenticated' };
	// The API answers 404 for a non-member so it never confirms a team
	// exists; treating 403 differently from 404 here would leak exactly what
	// internal/authz withholds.
	if (status === 403 || status === 404) return { kind: 'notFound' };
	if (status === 429) return { kind: 'rateLimited' };

	if (status === 409) {
		const count = blockingLinkCountOf(error);
		if (count !== undefined) return { count, kind: 'domainHasLinks' };
		if (isSlugConflict(error)) return { kind: 'slugTaken' };
	}

	if (status === 400 || status === 422) {
		const fields = fieldsOf(error);
		if (Object.keys(fields).length > 0) return { fields, kind: 'fields' };
	}

	return { kind: 'unknown' };
}
