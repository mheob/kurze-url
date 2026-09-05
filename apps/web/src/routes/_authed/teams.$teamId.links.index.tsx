import { createFileRoute } from '@tanstack/react-router';

/**
 * Placeholder only — Task 9 replaces this with the real link list
 * (`GET /v1/teams/{team_id}/links` rendered as a table, per the plan).
 *
 * It exists so `_authed` has at least one child with a real path segment.
 * `_authed.tsx` is a pathless layout (leading `_`); with no children at all
 * its own inferred full path collapses to "/", identical to the public
 * `routes/index.tsx`, and `@tanstack/router-generator` rejects that as a
 * duplicate route — confirmed by running the real build both ways (see
 * "Fix round 1" in task-6-report.md). Adding a child with a real segment
 * (`teams/$teamId/links`) is what gives `_authed` its own distinct full path
 * and makes the tree valid.
 *
 * `teamId` is rendered as-is: a technical identifier, not prose, the same
 * reasoning `SiteFooter` already applies to the untranslated `apiStatus`
 * value — so this needs no i18n key.
 */
export const Route = createFileRoute('/_authed/teams/$teamId/links/')({
	component: LinksPlaceholder,
});

function LinksPlaceholder(): React.JSX.Element {
	const { teamId } = Route.useParams();
	return <p>{teamId}</p>;
}
