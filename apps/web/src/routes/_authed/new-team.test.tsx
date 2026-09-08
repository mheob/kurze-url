import type { Team } from '@kurze-url/api-client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	isNotFound,
	Outlet,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it, vi } from 'vitest';

import { createI18n } from '../../i18n';
import type { Me } from '../_authed';
import { assertMaintainer, RouteComponent, validateSlugField } from './new-team';

/**
 * `createTeamFn` is a `createServerFn`, unreachable under Vitest ("No Start
 * context found") the same way every other server function in this app is —
 * `login.test.tsx` and `server/auth.test.ts` settled on `vi.hoisted` plus a
 * narrowly-typed fake for exactly this reason, so the submit tests below
 * follow the same mechanics rather than inventing a second way to fake a
 * server call.
 */
const mocks = vi.hoisted(() => ({
	createTeamFn: vi.fn<(input: { data: { name: string; slug: string } }) => Promise<Team>>(),
}));

vi.mock('../../server/teams', () => ({ createTeamFn: mocks.createTeamFn }));

const translate = (key: string): string => key;

function me(isMaintainer: boolean): Me {
	return {
		email: 'a@example.test',
		is_maintainer: isMaintainer,
		memberships: [],
		user_id: 'u1',
	};
}

/**
 * Same shape as `_authed.test.ts`'s helper, and for the same reason: vitest's
 * `no-conditional-expect` is error-level, and an assertion inside a `catch`
 * silently does not run when nothing was thrown.
 */
function thrown(fn: () => void): unknown {
	try {
		fn();
		return undefined;
	} catch (error) {
		return error;
	}
}

describe('assertMaintainer', () => {
	it('lets a maintainer through', () => {
		expect(() => assertMaintainer(me(true))).not.toThrow();
	});

	/**
	 * `isNotFound`, not a bare `.toThrow()`: swapping `notFound()` for any
	 * other error would leave a weaker assertion green. The distinction is
	 * deliberate — `requireTeamId` in the same tree answers 404 rather than
	 * 403, and a second guard next to it answering differently is what later
	 * gets copied into a route where the difference does leak something.
	 */
	it('throws a not-found, not a generic error, for everyone else', () => {
		expect(isNotFound(thrown(() => assertMaintainer(me(false))))).toBe(true);
	});
});

describe('validateSlugField', () => {
	it('accepts a well-formed slug', () => {
		expect(validateSlugField('sv-gruenwald', translate)).toBeUndefined();
	});

	it('reports an empty value as required, not as malformed', () => {
		expect(validateSlugField('  ', translate)).toBe('teams.slugRequired');
	});

	/**
	 * The same regex and bounds the Go API enforces via its Huma `pattern` tag
	 * and the database enforces via `team_slug_format`. Validating here is not
	 * a substitute for either — it saves a round trip that would otherwise
	 * answer 422 for a value the maintainer can see is wrong.
	 */
	it('reports a malformed or out-of-range value', () => {
		for (const malformed of ['SV-Gruenwald', 'sv_gruenwald', '-leading', 'trailing-', 'ab']) {
			expect(validateSlugField(malformed, translate)).toBe('teams.slugInvalid');
		}
	});

	it('accepts the shortest and the longest allowed lengths', () => {
		expect(validateSlugField('abc', translate)).toBeUndefined();
		expect(validateSlugField('a'.repeat(40), translate)).toBeUndefined();
	});

	it('rejects a value one character past the longest allowed length', () => {
		expect(validateSlugField('a'.repeat(41), translate)).toBe('teams.slugInvalid');
	});

	/**
	 * Distinguishes measuring length before trimming from after: the pattern
	 * alone accepts "a", so an implementation that checked `value.length`
	 * (3, counting the surrounding spaces) instead of the trimmed length would
	 * wrongly call this valid — none of the cases above would have noticed.
	 */
	it('rejects a value that is only a single character once trimmed', () => {
		expect(validateSlugField(' a ', translate)).toBe('teams.slugInvalid');
	});
});

/**
 * `RouteComponent` is rendered directly (not through the route's own
 * `beforeLoad`/`Route`), the same way `login.test.tsx` renders `LoginForm`
 * directly — this component takes no props and reads no route context, so a
 * minimal router (for `useRouter()`) and a `QueryClient` (for `useMutation`)
 * are the only extra scaffolding `TeamSwitcher`'s and `error-reporting`'s own
 * minimal-router pattern didn't already need.
 *
 * Also registers `/teams/$teamSlug/links` as a real destination and returns
 * the `router` itself alongside the render result: `onSuccess` reaches that
 * route with an imperative `router.navigate`, not a rendered `<Link>` whose
 * `href` a test could read directly, so proving where it actually lands means
 * letting the navigation finish and then inspecting
 * `router.state.location.pathname`.
 */
function renderNewTeamForm() {
	const queryClient = new QueryClient();
	const rootRoute = createRootRoute({ component: () => <Outlet /> });
	const indexRoute = createRoute({
		component: () => <RouteComponent />,
		getParentRoute: () => rootRoute,
		path: '/',
	});
	const linksRoute = createRoute({
		component: () => null,
		getParentRoute: () => rootRoute,
		path: '/teams/$teamSlug/links',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([indexRoute, linksRoute]),
	});

	return {
		router,
		...render(
			<I18nextProvider i18n={createI18n('en')}>
				<QueryClientProvider client={queryClient}>
					<RouterProvider router={router} />
				</QueryClientProvider>
			</I18nextProvider>,
		),
	};
}

describe('name-to-slug suggestion wiring', () => {
	/**
	 * The finding this fix closes: writing the suggestion through
	 * `form.setFieldValue` marked the slug field touched as a side effect,
	 * which is the exact signal the guard reads to decide whether to keep
	 * suggesting — so the previous wiring stopped following after the very
	 * first keystroke. Asserting only after the *full* name is typed is
	 * deliberate: a single-character name would have passed even with that
	 * bug, since one keystroke is exactly what still worked.
	 */
	it('updates the slug suggestion on every keystroke, not just the first', async () => {
		renderNewTeamForm();

		await userEvent.type(await screen.findByLabelText('Team name'), 'Sportverein Grünwald');

		expect(screen.getByLabelText('URL name')).toHaveValue('sportverein-gruenwald');
	});

	it('stops following the name once the maintainer edits the slug themselves', async () => {
		renderNewTeamForm();

		await userEvent.type(await screen.findByLabelText('Team name'), 'Sportverein Grünwald');
		const slugInput = screen.getByLabelText('URL name');
		await userEvent.clear(slugInput);
		await userEvent.type(slugInput, 'custom-slug');

		await userEvent.type(screen.getByLabelText('Team name'), ' e.V.');

		expect(slugInput).toHaveValue('custom-slug');
	});

	it('shows no slug validation error while the maintainer is only typing the name', async () => {
		renderNewTeamForm();

		await userEvent.type(await screen.findByLabelText('Team name'), 'Sportverein Grünwald');

		expect(screen.queryByRole('alert')).not.toBeInTheDocument();
	});
});

/**
 * Fills in and submits the form with a name whose suggested slug ("Verein A"
 * → "verein-a") is used as-is, so every test below can assert on that fixed
 * value without also exercising the suggestion wiring above.
 */
async function submitForm(): Promise<void> {
	await userEvent.type(await screen.findByLabelText('Team name'), 'Verein A');
	await userEvent.click(screen.getByRole('button', { name: 'Save' }));
}

describe('submitting the form', () => {
	/**
	 * `team.slug` and `team.id` are both typed `string`, and `params` is a
	 * string-keyed record — nothing in the type system stops `onSuccess` from
	 * navigating with the wrong one, and a suite that never submits the form
	 * would stay green either way. Giving the fake response an `id` that looks
	 * nothing like its `slug` is what makes this fail if `new-team.tsx` were
	 * changed to navigate with `team.id`: the assertion below pins the actual
	 * path, not just that a navigation happened.
	 */
	it('navigates to the created team by slug, not by id', async () => {
		mocks.createTeamFn.mockResolvedValueOnce({
			created_at: '2026-01-01T00:00:00Z',
			id: 'a1b2c3d4-team-uuid',
			name: 'Verein A',
			role: 'owner',
			slug: 'verein-a',
		});
		const { router } = renderNewTeamForm();

		await submitForm();

		await waitFor(() => expect(router.state.location.pathname).toBe('/teams/verein-a/links'));
	});

	/**
	 * `new-team.tsx` deliberately keeps a taken slug off the page banner: the
	 * banner renders `t(\`errors.${failure.kind}\`)` for every kind but
	 * `fields`, and `errors.slugTaken` exists in neither catalogue (see the
	 * comment on `isSlugConflict` in `lib/api-errors.ts`). `classifyApiError`
	 * itself is covered by its own unit tests; nothing before this rendered
	 * the 409 through the actual form to prove the field gets the message and
	 * the banner stays silent.
	 */
	it('shows a taken slug on the field, not as a page banner', async () => {
		mocks.createTeamFn.mockRejectedValueOnce({
			errors: [{ location: 'body.slug', message: 'this slug is already taken' }],
			status: 409,
			title: 'Conflict',
		});
		renderNewTeamForm();

		await submitForm();

		// The translated message, on the slug field's own error paragraph
		// (`id="slug-error"`) — and, crucially, the *only* alert on the page:
		// if `formMessage` also rendered its `<p role="alert">` banner (the
		// regression this test guards against), there would be two.
		const slugError = await screen.findByText(
			'That URL name is already taken. Please choose another.',
		);
		expect(slugError).toHaveAttribute('id', 'slug-error');
		expect(screen.getAllByRole('alert')).toHaveLength(1);
	});
});
