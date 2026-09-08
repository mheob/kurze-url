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
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';

import { createI18n } from '../../i18n';
import type { Me } from '../_authed';
import { assertMaintainer, RouteComponent, validateSlugField } from './new-team';

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
	 * deliberate — `assertMembership` in the same tree answers 404 rather than
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
 */
function renderNewTeamForm(): ReturnType<typeof render> {
	const queryClient = new QueryClient();
	const rootRoute = createRootRoute({ component: () => <Outlet /> });
	const indexRoute = createRoute({
		component: () => <RouteComponent />,
		getParentRoute: () => rootRoute,
		path: '/',
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: ['/'] }),
		routeTree: rootRoute.addChildren([indexRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<QueryClientProvider client={queryClient}>
				<RouterProvider router={router} />
			</QueryClientProvider>
		</I18nextProvider>,
	);
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
