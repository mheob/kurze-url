/* oxlint-disable no-script-url -- `javascript:` is the hostile value this file exists to refuse: a redirect the page must never follow. */

import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	isRedirect,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vite-plus/test';

import { createI18n } from '../i18n';
import { isSafeNext } from '../lib/safe-next';
import type { ConsentDecision, ConsentState } from '../server/oauth-consent';

/**
 * The two server functions, replaced by fakes typed with only what the route passes them. The real
 * module reaches `getRequest()`, which has no Start context under Vitest; its own behaviour is
 * `server/oauth-consent.test.ts`'s to test.
 */
const mocks = vi.hoisted(() => ({
	decideConsent:
		vi.fn<
			(
				input: Readonly<{ data: Readonly<{ action: string; authorizationId: string }> }>,
			) => Promise<ConsentDecision>
		>(),
	loadConsent:
		vi.fn<
			(input: Readonly<{ data: Readonly<{ authorizationId: string }> }>) => Promise<ConsentState>
		>(),
}));

vi.mock('../server/oauth-consent', () => ({
	decideConsent: mocks.decideConsent,
	loadConsent: mocks.loadConsent,
}));

// oxlint-disable-next-line node/no-top-level-await -- `vi.mock` above is hoisted; importing the subject module only after it, at module scope, is Vitest's own documented way to get a mocked dependency into an ESM import, as `login.test.tsx` does.
const { loadConsentPage, Route } = await import('./oauth.consent');

const CLI_URL = 'http://127.0.0.1:41234/callback?code=the-code&state=s';
const CONSENT: ConsentState = {
	authorizationId: 'abc',
	clientName: 'kurze-url CLI',
	email: 'sabine@sv-gruenwald.example',
	kind: 'consent',
};
const LOGIN_NEXT = '/oauth/consent?authorization_id=abc';
const LOGIN_MARKER = 'login page';

/**
 * Stands in for `/login`, which is not under test: where the visitor lands, and with what search,
 * is read off the router, so the page only has to exist for the navigation to complete.
 *
 * @returns A marker paragraph.
 */
function LoginStandIn(): React.JSX.Element {
	return <p>{LOGIN_MARKER}</p>;
}

/** Where the router is, read when a test asks and not before. */
interface Landing {
	readonly href: string;
	readonly pathname: string;
	readonly search: Readonly<Record<string, unknown>>;
}

/**
 * Mounts the page the real `/oauth/consent` route declares, with a stand-in `/login`, so what is
 * under test is the route's own `validateSearch`, `loaderDeps`, `loader` and `component`. The route
 * is hung under this test's own root the way `routeTree.gen.ts` hangs it under the real one
 * (`Route.update(...)` is `Object.assign(Route.options, ...)` without the parent-route typing),
 * the same trick `teams.$teamSlug.domains.role.test.tsx` uses, and not rebuilt with
 * `createRoute`, which would put the loader's own context types in conflict with a bare root.
 *
 * @param url - The URL to start at, with its query string.
 * @returns A reader of where the page has ended up.
 */
function renderConsentRoute(url: string): () => Landing {
	const rootRoute = createRootRoute();
	Object.assign(Route.options, { getParentRoute: () => rootRoute, path: '/oauth/consent' });
	const loginRoute = createRoute({
		component: LoginStandIn,
		getParentRoute: () => rootRoute,
		path: '/login',
		validateSearch: (search: Readonly<Record<string, unknown>>) => ({
			next: typeof search.next === 'string' ? search.next : undefined,
		}),
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: [url] }),
		routeTree: rootRoute.addChildren([Route, loginRoute]),
	});

	render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
	return () => {
		const { href, pathname, search } = router.state.location;
		return { href, pathname, search: { ...search } };
	};
}

/**
 * What a loader threw, as a redirect's options, or undefined when it threw something else or
 * nothing. `redirect()` is TanStack Router's navigation: a thrown object, not an `Error`.
 *
 * @param authorizationId - The id the loader is given.
 * @returns The thrown redirect's `href` and `to`.
 */
async function redirectedBy(
	authorizationId: string,
): Promise<{ readonly href?: unknown; readonly to?: unknown } | undefined> {
	try {
		await loadConsentPage(authorizationId);
	} catch (error) {
		// `unknown` fields: the router types a redirect's options loosely, and what is asserted on is
		// their value, not their type.
		return isRedirect(error) ? { href: error.options.href, to: error.options.to } : undefined;
	}
	return undefined;
}

/** A decision the test settles later, while the page shows it as pending. */
interface PendingDecision {
	readonly promise: Promise<ConsentDecision>;
	readonly resolve: (decision: ConsentDecision) => void;
}

/**
 * A decision that stays in flight until the test resolves it. Not `Promise.withResolvers`:
 * `apps/web`'s `lib` stops at ES2022.
 *
 * @returns The pending promise and the function that settles it.
 */
function pendingDecision(): PendingDecision {
	let settle: ((decision: ConsentDecision) => void) | undefined;
	// oxlint-disable-next-line promise/avoid-new -- a deliberately deferred promise: the test resolves it later, in response to what the UI does while the request is still in flight, the same reason `login.test.tsx` gives.
	const promise = new Promise<ConsentDecision>((resolve) => {
		settle = resolve;
	});
	return {
		promise,
		resolve: (decision: ConsentDecision) => {
			settle?.(decision);
		},
	};
}

/**
 * Loads the page in its consent state, ready for a decision.
 *
 * @returns A reader of where the page has ended up.
 */
async function loadedConsent(): Promise<() => Landing> {
	mocks.loadConsent.mockResolvedValue(CONSENT);
	const landing = renderConsentRoute('/oauth/consent?authorization_id=abc');
	await screen.findByRole('button', { name: 'Allow' });
	return landing;
}

describe('the consent route', () => {
	let assign: ReturnType<typeof vi.fn<(url: string) => void>>;

	beforeEach(() => {
		mocks.decideConsent.mockReset();
		mocks.loadConsent.mockReset();
		// jsdom cannot navigate, and `location.assign` is not replaceable on its own object: the
		// whole `location` is, which is what `globalThis.location.assign` then reads. Only what the
		// router reads of it at construction is carried over.
		assign = vi.fn<(url: string) => void>();
		const { href, origin } = globalThis.location;
		vi.stubGlobal('location', { assign, href, origin });
	});

	afterEach(() => {
		vi.unstubAllGlobals();
	});

	describe('when the page is loaded', () => {
		// Review Focus 2: a missing, empty or odd id is an ordinary thing to arrive with. It renders
		// the error card, throws nothing (so Sentry hears nothing) and asks nobody.
		it.each([
			['no authorization_id', '/oauth/consent'],
			['an empty authorization_id', '/oauth/consent?authorization_id='],
			// TanStack parses a bare number out of the query string; only a string is an id.
			['a numeric authorization_id', '/oauth/consent?authorization_id=42'],
		])('renders the error card for %s, without asking the server', async (_name, url) => {
			renderConsentRoute(url);

			await expect(
				screen.findByRole('heading', { name: "This request can't be completed" }),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('button')).not.toBeInTheDocument();
			expect(mocks.loadConsent).not.toHaveBeenCalled();
		});

		it('renders the error card for an authorization the server calls invalid', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'invalid' });
			renderConsentRoute('/oauth/consent?authorization_id=gone');

			await expect(
				screen.findByRole('heading', { name: "This request can't be completed" }),
			).resolves.toBeInTheDocument();
			expect(mocks.loadConsent).toHaveBeenCalledExactlyOnceWith({
				data: { authorizationId: 'gone' },
			});
		});

		// A page that loads invalid has not been interacted with, so it must not take focus.
		it('leaves focus alone for a page that loads invalid', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'invalid' });
			renderConsentRoute('/oauth/consent?authorization_id=gone');

			const heading = await screen.findByRole('heading', {
				name: "This request can't be completed",
			});

			expect(heading).not.toHaveFocus();
			expect(document.body).toHaveFocus();
		});

		it('renders the request for the client and the person it names', async () => {
			mocks.loadConsent.mockResolvedValue(CONSENT);
			renderConsentRoute('/oauth/consent?authorization_id=abc');

			await expect(
				screen.findByRole('heading', { name: 'Allow CLI access' }),
			).resolves.toBeInTheDocument();
			expect(
				screen.getByText('kurze-url CLI wants to access kurze-url on your behalf.'),
			).toBeInTheDocument();
			expect(screen.getByText('Signed in as sabine@sv-gruenwald.example')).toBeInTheDocument();
			expect(mocks.loadConsent).toHaveBeenCalledExactlyOnceWith({
				data: { authorizationId: 'abc' },
			});
		});

		it('sends a signed-out visitor to /login, carrying the consent path as next', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'unauthenticated' });
			const landing = renderConsentRoute('/oauth/consent?authorization_id=abc');

			await expect(screen.findByText(LOGIN_MARKER)).resolves.toBeInTheDocument();
			expect(landing().pathname).toBe('/login');
			expect(landing().search).toStrictEqual({ next: LOGIN_NEXT });
			expect(landing().href).toBe('/login?next=%2Foauth%2Fconsent%3Fauthorization_id%3Dabc');
		});

		// `/auth/callback` follows only what `isSafeNext` accepts, so a `next` this route builds that
		// it refuses would silently drop the visitor on `/` after signing in. An id with characters
		// that need escaping is where that would show.
		it.each(['abc', 'a b&c=d/e?f#g', 'ä ö ü', '2f1c9d4e-7a2b-4c3d-8e5f-0123456789ab'])(
			'builds a next that the callback will follow, for the id %j',
			async (authorizationId) => {
				mocks.loadConsent.mockResolvedValue({ kind: 'unauthenticated' });
				const landing = renderConsentRoute(
					`/oauth/consent?authorization_id=${encodeURIComponent(authorizationId)}`,
				);

				await screen.findByText(LOGIN_MARKER);
				const { next } = landing().search;
				expect(next).toBe(`/oauth/consent?authorization_id=${encodeURIComponent(authorizationId)}`);
				expect(isSafeNext(next)).toBe(true);
			},
		);

		it('hands an already consented authorization on to the CLI', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'redirect', url: CLI_URL });

			await expect(redirectedBy('abc')).resolves.toStrictEqual({ href: CLI_URL, to: undefined });
			expect(mocks.loadConsent).toHaveBeenCalledExactlyOnceWith({
				data: { authorizationId: 'abc' },
			});
		});

		it('builds the same login redirect the rendered route follows', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'unauthenticated' });

			await expect(redirectedBy('abc')).resolves.toStrictEqual({ href: undefined, to: '/login' });
		});

		// The controller's ruling: a redirect is followed only when it is an http or https URL. A
		// `javascript:` URL in `Location` or `location.assign` is script execution on this origin,
		// and nothing about Supabase's answer is ours to vouch for.
		it.each([
			'javascript:alert(1)',
			'data:text/html,<script>alert(1)</script>',
			'ftp://example.test/file',
			'not a url',
			'//no-scheme.example/path',
		])('refuses to follow the redirect %j', async (url) => {
			mocks.loadConsent.mockResolvedValue({ kind: 'redirect', url });

			await expect(loadConsentPage('abc')).resolves.toStrictEqual({ kind: 'invalid' });
		});

		it('renders the error card for a redirect that is not http or https', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'redirect', url: 'javascript:alert(1)' });
			renderConsentRoute('/oauth/consent?authorization_id=abc');

			await expect(
				screen.findByRole('heading', { name: "This request can't be completed" }),
			).resolves.toBeInTheDocument();
			expect(assign).not.toHaveBeenCalled();
		});

		it('follows an https redirect as it follows an http one', async () => {
			mocks.loadConsent.mockResolvedValue({ kind: 'redirect', url: 'https://cli.example/cb' });

			await expect(redirectedBy('abc')).resolves.toStrictEqual({
				href: 'https://cli.example/cb',
				to: undefined,
			});
		});
	});

	describe('when a decision is made', () => {
		it('sends the approval and follows the CLI redirect it is given', async () => {
			mocks.decideConsent.mockResolvedValue({ kind: 'redirect', url: CLI_URL });
			await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

			await waitFor(() => {
				expect(assign).toHaveBeenCalledExactlyOnceWith(CLI_URL);
			});
			expect(mocks.decideConsent).toHaveBeenCalledExactlyOnceWith({
				data: { action: 'approve', authorizationId: 'abc' },
			});
			// The page is on its way out, so a second decision stays impossible.
			expect(screen.getByRole('button', { name: 'Allow' })).toBeDisabled();
			expect(screen.getByRole('button', { name: 'Deny' })).toBeDisabled();
		});

		it('sends the denial and follows the redirect it is given', async () => {
			mocks.decideConsent.mockResolvedValue({
				kind: 'redirect',
				url: 'http://127.0.0.1:41234/callback?error=access_denied',
			});
			await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Deny' }));

			await waitFor(() => {
				expect(assign).toHaveBeenCalledExactlyOnceWith(
					'http://127.0.0.1:41234/callback?error=access_denied',
				);
			});
			expect(mocks.decideConsent).toHaveBeenCalledExactlyOnceWith({
				data: { action: 'deny', authorizationId: 'abc' },
			});
		});

		// Review Focus 3: both buttons are disabled while the decision is in flight, so a double
		// click sends one decision.
		it('sends one decision for a double click on Allow', async () => {
			const pending = pendingDecision();
			mocks.decideConsent.mockReturnValue(pending.promise);
			await loadedConsent();

			await userEvent.dblClick(screen.getByRole('button', { name: 'Allow' }));

			expect(screen.getByRole('button', { name: 'Allow' })).toBeDisabled();
			expect(screen.getByRole('button', { name: 'Deny' })).toBeDisabled();
			await userEvent.click(screen.getByRole('button', { name: 'Deny' }));
			expect(mocks.decideConsent).toHaveBeenCalledOnce();

			pending.resolve({ kind: 'redirect', url: CLI_URL });
			await waitFor(() => {
				expect(assign).toHaveBeenCalledExactlyOnceWith(CLI_URL);
			});
		});

		// Review Focus 4: the session ended between loading the page and pressing Allow. The server
		// says so, and the visitor goes to the same login URL a signed-out load would have sent them
		// to, instead of seeing a failure after pressing the button.
		it('sends the visitor to /login with the same next when the session has ended', async () => {
			mocks.decideConsent.mockResolvedValue({ kind: 'unauthenticated' });
			const landing = await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

			await expect(screen.findByText(LOGIN_MARKER)).resolves.toBeInTheDocument();
			expect(landing().pathname).toBe('/login');
			expect(landing().search).toStrictEqual({ next: LOGIN_NEXT });
			expect(isSafeNext(landing().search.next)).toBe(true);
			expect(assign).not.toHaveBeenCalled();
		});

		it('replaces the request with the error card when the authorization is gone', async () => {
			mocks.decideConsent.mockResolvedValue({ kind: 'invalid' });
			await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

			await expect(
				screen.findByRole('heading', { name: "This request can't be completed" }),
			).resolves.toBeInTheDocument();
			expect(screen.queryByRole('button')).not.toBeInTheDocument();
			expect(assign).not.toHaveBeenCalled();
		});

		// Pressing Allow disables the focused button and the request then turns out to be gone, which
		// replaces the card: focus would fall to `<body>` and nothing would be announced (WCAG 2.4.3,
		// 4.1.3), so it moves to the heading that now names the new state.
		it('moves focus to the heading when the authorization turns out to be gone', async () => {
			mocks.decideConsent.mockResolvedValue({ kind: 'invalid' });
			await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

			const heading = await screen.findByRole('heading', {
				name: "This request can't be completed",
			});
			await waitFor(() => {
				expect(heading).toHaveFocus();
			});
		});

		it('refuses to follow a decision that redirects somewhere other than http or https', async () => {
			mocks.decideConsent.mockResolvedValue({ kind: 'redirect', url: 'javascript:alert(1)' });
			await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

			await expect(
				screen.findByRole('heading', { name: "This request can't be completed" }),
			).resolves.toBeInTheDocument();
			expect(assign).not.toHaveBeenCalled();
		});

		// A decision that threw (the server is down, the network dropped) is a real failure and not
		// a state to render: it goes to the error boundary, which is where the app reports one, and
		// not into a swallowed rejection that leaves both buttons disabled for good.
		it('hands a failed decision to the error boundary', async () => {
			mocks.decideConsent.mockRejectedValue(new Error('the decision failed'));
			await loadedConsent();

			await userEvent.click(screen.getByRole('button', { name: 'Allow' }));

			await expect(screen.findByText(/the decision failed/u)).resolves.toBeInTheDocument();
			expect(screen.queryByRole('button', { name: 'Allow' })).not.toBeInTheDocument();
			expect(assign).not.toHaveBeenCalled();
		});
	});
});
