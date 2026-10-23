import {
	createMemoryHistory,
	createRootRoute,
	createRoute,
	createRouter,
	RouterProvider,
} from '@tanstack/react-router';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { I18nextProvider } from 'react-i18next';
import { beforeEach, describe, expect, it, vi } from 'vite-plus/test';

import { createI18n } from '../i18n';

/**
 * `vi.hoisted` plus a narrowly-typed fake, the same mechanics
 * `server/auth.test.ts` and `server/session.test.ts` settled on: a bare
 * `const sendMagicLink = vi.fn(...)` referenced from `vi.mock`'s factory
 * happens to work here too, but `vi.hoisted` is what makes that not an
 * accident of hoisting order.
 */
const mocks = vi.hoisted(() => ({
	sendMagicLink: vi.fn<
		(
			input: Readonly<{ data: Readonly<{ email: string; next?: string }> }>,
		) => Promise<{ sent: true }>
		// oxlint-disable-next-line typescript/require-await -- stands in for `sendMagicLink`, which returns a `Promise`; nothing here needs an `await`.
	>(async () => ({ sent: true })),
}));

vi.mock('../server/auth', () => ({ sendMagicLink: mocks.sendMagicLink }));

// oxlint-disable-next-line node/no-top-level-await -- `vi.mock` above is hoisted; importing the subject module only after it, at module scope, is Vitest's own documented way to get a mocked dependency into an ESM import — the same pattern every other `*.test.ts(x)` in this app that mocks an import uses.
const { LoginForm, Route } = await import('./login');

/**
 * `LoginForm` reads every string through `useTranslation`, which needs an
 * `I18nextProvider` in scope — without one, `t()` has no instance to draw
 * from and renders the raw key. `language-switcher.test.tsx` settled on the
 * same `createI18n` + `I18nextProvider` wrapper for the same reason.
 *
 * @returns The render result.
 */
function renderLoginForm(): ReturnType<typeof render> {
	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<LoginForm />
		</I18nextProvider>,
	);
}

describe('loginForm', () => {
	it('shows the same confirmation whatever the address', async () => {
		renderLoginForm();
		await userEvent.type(screen.getByLabelText(/email|e-mail/iu), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/iu }));

		await expect(screen.findByText(/on its way|unterwegs/iu)).resolves.toBeInTheDocument();
	});

	it('puts the form inside a main landmark', () => {
		// axe's `region` rule fails any content outside every landmark and
		// `landmark-one-main` wants exactly one `<main>` per document. This page
		// renders no other chrome, so the form itself has to be inside it — a
		// bare `<form>` here means a screen reader user has no landmark to jump
		// to and the e2e accessibility scan fails the whole page.
		renderLoginForm();
		expect(screen.getByRole('main')).toContainElement(
			screen.getByRole('button', { name: /link/iu }),
		);
	});

	it('labels the field, so it is reachable without a mouse', () => {
		// An input with only a placeholder passes a visual review and fails a
		// screen reader. Accessibility is a CI gate here, not a preference.
		renderLoginForm();
		expect(screen.getByLabelText(/email|e-mail/iu)).toBeInTheDocument();
	});

	/**
	 * Finding 5: `createSupabase` throws outright when `SUPABASE_URL` or
	 * `SUPABASE_PUBLISHABLE_KEY` is unset, and the RPC itself can reject on a
	 * plain network failure. Before this fix, an uncaught rejection here left
	 * the button looking clicked with no feedback, forever — the same
	 * `aria-live` region the success path already uses is what surfaces it,
	 * not a second, separate error area.
	 */
	it('tells the visitor something went wrong instead of doing nothing', async () => {
		mocks.sendMagicLink.mockRejectedValueOnce(new Error('boom'));
		renderLoginForm();
		await userEvent.type(screen.getByLabelText(/email|e-mail/iu), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/iu }));

		await expect(screen.findByText(/went wrong|schiefgelaufen/iu)).resolves.toBeInTheDocument();
		expect(screen.queryByText(/on its way|unterwegs/iu)).not.toBeInTheDocument();
	});

	/**
	 * Without this, the ~1s enumeration-timing floor `ENUMERATION_TIMING_FLOOR_MS`
	 * defends (`server/auth.ts`) reads as a dead second on screen, and nothing
	 * stops a second click from firing a second request in that window.
	 */
	it('disables the submit button until the request settles', async () => {
		let resolveSend: (() => void) | undefined;
		mocks.sendMagicLink.mockImplementationOnce(
			async () =>
				// oxlint-disable-next-line promise/avoid-new -- a deliberately deferred promise: the test resolves it later, from the `resolveSend` closure, in response to what the UI does while the request is still in flight. There is no async/await equivalent for "resolve this from arbitrary code, later."
				new Promise((resolve) => {
					resolveSend = () => {
						resolve({ sent: true });
					};
				}),
		);
		renderLoginForm();
		await userEvent.type(screen.getByLabelText(/email|e-mail/iu), 'a@example.test');
		const button = screen.getByRole('button', { name: /link/iu });
		await userEvent.click(button);

		expect(button).toBeDisabled();

		resolveSend?.();
		await screen.findByText(/on its way|unterwegs/iu);
		expect(button).not.toBeDisabled();
	});
});

/**
 * Mounts the page the `/login` route declares, at the URL a signed-out visitor to the consent
 * page is sent to. The route is rebuilt from its own `component` and `validateSearch` rather than
 * imported through `routeTree.gen.ts`, which would drag in the whole document shell: what is under
 * test is how the search string reaches the form, and both halves of that are the route's own.
 *
 * @param url - The `/login` URL to start at, with its query string.
 * @returns The rendered test utilities from Testing Library's `render`.
 */
function renderLoginRoute(url: string): ReturnType<typeof render> {
	const rootRoute = createRootRoute();
	const loginRoute = createRoute({
		component: Route.options.component,
		getParentRoute: () => rootRoute,
		path: '/login',
		validateSearch: Route.options.validateSearch,
	});
	const router = createRouter({
		history: createMemoryHistory({ initialEntries: [url] }),
		routeTree: rootRoute.addChildren([loginRoute]),
	});

	return render(
		<I18nextProvider i18n={createI18n('en')}>
			<RouterProvider router={router} />
		</I18nextProvider>,
	);
}

describe('the login route', () => {
	beforeEach(() => {
		mocks.sendMagicLink.mockClear();
	});

	/**
	 * A CLI login lands a signed-out visitor on `/login?next=<consent path>`. The form has to hand
	 * that value to the server function, which is the one that decides whether to trust it and puts
	 * it in the magic link.
	 */
	it('sends the next parameter along with the address', async () => {
		renderLoginRoute('/login?next=%2Foauth%2Fconsent%3Fauthorization_id%3Dabc');
		await userEvent.type(await screen.findByLabelText(/email|e-mail/iu), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/iu }));

		await screen.findByText(/on its way|unterwegs/iu);
		expect(mocks.sendMagicLink).toHaveBeenCalledExactlyOnceWith({
			data: { email: 'a@example.test', next: '/oauth/consent?authorization_id=abc' },
		});
	});

	it('sends no next when the URL has none', async () => {
		renderLoginRoute('/login');
		await userEvent.type(await screen.findByLabelText(/email|e-mail/iu), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/iu }));

		await screen.findByText(/on its way|unterwegs/iu);
		expect(mocks.sendMagicLink).toHaveBeenCalledOnce();
		expect(mocks.sendMagicLink.mock.calls[0]?.[0].data.next).toBeUndefined();
	});

	// The search string can hold anything, and TanStack parses a bare number or a JSON literal out
	// of it: only a string is a candidate for `next`, and the server function judges that string.
	it('ignores a next that is not a string', async () => {
		renderLoginRoute('/login?next=42');
		await userEvent.type(await screen.findByLabelText(/email|e-mail/iu), 'a@example.test');
		await userEvent.click(screen.getByRole('button', { name: /link/iu }));

		await screen.findByText(/on its way|unterwegs/iu);
		expect(mocks.sendMagicLink.mock.calls[0]?.[0].data.next).toBeUndefined();
	});
});
