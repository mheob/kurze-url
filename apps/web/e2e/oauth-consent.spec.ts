import { createHash, randomBytes, randomUUID } from 'node:crypto';

// Named import, not the default: the package exports the same class both ways
// (`export { AxeBuilder, AxeBuilder as default }`), and oxlint's
// `import/no-named-as-default` flags a default import bound to the same name
// as an existing named export as confusing. Same note as `folders.spec.ts`.
import { AxeBuilder } from '@axe-core/playwright';
import { expect, type Page } from '@playwright/test';

import { test as authTest } from './fixtures/auth';
import { requireE2eEnv } from './fixtures/env';
import { waitForHydration } from './fixtures/hydration';

/* oxlint-disable typescript/prefer-readonly-parameter-types -- every finding of this rule in this
 * file is Playwright's own `Page`/`Route` (bare, or nested inside the fixture argument object each
 * `test` callback destructures); both have mutating methods (`goto`, `click`, `fulfill`, ...) and
 * neither type is ours to edit. Same note as `domains.spec.ts` and `links.spec.ts`.
 */

/**
 * Drives `/oauth/consent` the way a CLI login does, against the real Supabase OAuth 2.1 server of
 * whichever project `SUPABASE_URL` names (the Preview project in CI, the local stack on a
 * workstation). It needs that server switched on, which is a dashboard setting on Preview: with it
 * off, registering the client below answers an error and every test here fails with that error
 * rather than passing around it.
 *
 * What is real and what is not. The authorization is real: it is started the way the CLI starts it,
 * with PKCE, against a client this file registers for the run. The consent page is the deployment
 * under test, signed in through `./fixtures/auth` as that test's own fresh user. The CLI's
 * loopback listener is not real: `http://127.0.0.1:53682/callback` is intercepted in the browser
 * and answered with a stub, because the point is what the page sends the browser to, and a
 * listener on a fixed port would also stop two runs from sharing a machine.
 *
 * Supabase sends a started authorization to the project's Site URL, which is not necessarily this
 * deployment (Preview's is not this pull request's preview), so the tests open
 * `/oauth/consent?authorization_id=…` on `baseURL` themselves, and use only the `authorization_id`
 * from the redirect Supabase answers with.
 *
 * Grants are per user and client. Every test gets a fresh user from the fixture, so a grant one
 * test stores can never skip the card in another, and none is revoked: the user, and with it the
 * grant, is deleted when the test ends. The client itself is deleted in `afterAll`.
 */

const CLIENT_NAME = 'kurze-url CLI e2e';
const LOOPBACK_ORIGIN = 'http://127.0.0.1:53682';
const REDIRECT_URI = `${LOOPBACK_ORIGIN}/callback`;
const LOOPBACK_PATTERN = `${LOOPBACK_ORIGIN}/**`;
// RFC 7636 asks for 43 to 128 characters; 32 random bytes are 43 in base64url.
const VERIFIER_BYTES = 32;
// A failure body is shown to explain a refusal, and nothing in an auth error carries a credential;
// the cap only keeps an HTML error page from filling the report.
const ERROR_BODY_LIMIT = 300;

interface Authorization {
	readonly id: string;
	readonly state: string;
}

/** The registered client's id, held between `beforeAll` and `afterAll` for the worker's tests. */
let registeredClientId: string | undefined = undefined;

/**
 * The headers of a call that needs the service role: Supabase's gateway wants the key as `apikey`,
 * and GoTrue's admin routes want the same key as the bearer token. The hosted gateway's own rule
 * about `apikey` has not been measured for the OAuth routes, so it is always sent.
 *
 * @param serviceRoleKey - The project's service-role key.
 * @returns The headers to send.
 */
function serviceRoleHeaders(serviceRoleKey: string): Record<string, string> {
	return {
		apikey: serviceRoleKey,
		authorization: `Bearer ${serviceRoleKey}`,
		'content-type': 'application/json',
	};
}

/**
 * Turns a refused call into an error that says which call it was and what Supabase answered.
 *
 * @param step - What the call was for.
 * @param response - The refusal.
 * @returns The error to throw.
 */
async function refusal(step: string, response: Readonly<Response>): Promise<Error> {
	const body = await response.text();
	return new Error(`${step} answered ${response.status}: ${body.slice(0, ERROR_BODY_LIMIT)}`);
}

/**
 * Reads `client_id` out of Supabase's answer to a client registration.
 *
 * @param body - The parsed JSON of the answer.
 * @returns The new client's id.
 */
function clientIdOf(body: unknown): string {
	if (
		typeof body === 'object' &&
		body !== null &&
		'client_id' in body &&
		typeof body.client_id === 'string'
	) {
		return body.client_id;
	}
	throw new Error('registering the OAuth client answered without a client_id');
}

/**
 * Registers the temporary public client the run authorizes against: PKCE-only (no secret), with
 * the CLI's loopback redirect as its only allowed redirect URI.
 *
 * @returns The new client's id.
 */
async function registerClient(): Promise<string> {
	const { serviceRoleKey, url } = requireE2eEnv();
	const response = await fetch(`${url}/auth/v1/admin/oauth/clients`, {
		body: JSON.stringify({
			client_name: CLIENT_NAME,
			grant_types: ['authorization_code', 'refresh_token'],
			redirect_uris: [REDIRECT_URI],
			response_types: ['code'],
			token_endpoint_auth_method: 'none',
		}),
		headers: serviceRoleHeaders(serviceRoleKey),
		method: 'POST',
	});
	if (!response.ok) {
		throw await refusal(
			'registering the OAuth client (is the OAuth server switched on for this Supabase project?)',
			response,
		);
	}
	return clientIdOf(await response.json());
}

/**
 * Deletes the client `registerClient` made, and with it every grant given to it.
 *
 * @param id - The client to delete.
 */
async function deleteClient(id: string): Promise<void> {
	const { serviceRoleKey, url } = requireE2eEnv();
	const response = await fetch(`${url}/auth/v1/admin/oauth/clients/${id}`, {
		headers: serviceRoleHeaders(serviceRoleKey),
		method: 'DELETE',
	});
	if (!response.ok) throw await refusal(`deleting the OAuth client ${id}`, response);
}

/**
 * The registered client's id, for a test that needs one.
 *
 * @returns The id `beforeAll` stored.
 */
function registeredClient(): string {
	if (registeredClientId === undefined) throw new Error('the OAuth client was never registered');
	return registeredClientId;
}

/**
 * Starts an authorization the way the CLI does: `GET /auth/v1/oauth/authorize` with an S256 PKCE
 * challenge and a state, not following the redirect. Supabase answers 302 to its consent URL when
 * the client and redirect URI are registered, and 400 when they are not.
 *
 * @returns The `authorization_id` from that redirect, and the `state` it was started with, which
 *   the redirect back to the CLI has to carry unchanged.
 */
async function startAuthorization(): Promise<Authorization> {
	const { serviceRoleKey, url } = requireE2eEnv();
	const state = randomUUID();
	const verifier = randomBytes(VERIFIER_BYTES).toString('base64url');
	const query = new URLSearchParams({
		client_id: registeredClient(),
		code_challenge: createHash('sha256').update(verifier).digest('base64url'),
		code_challenge_method: 'S256',
		redirect_uri: REDIRECT_URI,
		response_type: 'code',
		state,
	});
	const response = await fetch(`${url}/auth/v1/oauth/authorize?${query.toString()}`, {
		headers: { apikey: serviceRoleKey },
		redirect: 'manual',
	});
	const location = response.headers.get('location');
	if (location === null) throw await refusal('starting the authorization', response);
	const id = new URL(location, url).searchParams.get('authorization_id');
	if (id === null) throw new Error('the authorization redirect carried no authorization_id');
	return { id, state };
}

/**
 * The consent page's path for an authorization, as the CLI's browser would be sent to it. The id
 * is encoded the way the page itself encodes it when it sends a signed-out visitor to `/login`.
 *
 * @param authorization - The started authorization.
 * @returns The path and query, relative to `baseURL`.
 */
function consentPath(authorization: Readonly<Authorization>): string {
	return `/oauth/consent?authorization_id=${encodeURIComponent(authorization.id)}`;
}

/**
 * Answers every request to the CLI's loopback URL in the browser, so that nothing has to listen on
 * the port and the navigation to it still completes (and so `page.url()` can be read afterwards).
 *
 * @param page - The page whose navigations to intercept.
 */
async function interceptLoopback(page: Page): Promise<void> {
	await page.route(LOOPBACK_PATTERN, async (route) => {
		await route.fulfill({ body: 'The CLI listens here.', contentType: 'text/plain', status: 200 });
	});
}

/**
 * Opens the consent card for an authorization, waits until React owns the buttons (a click before
 * that does nothing, see `waitForHydration`), presses one, and waits for the browser to arrive at
 * the CLI's loopback URL.
 *
 * @param page - A signed-in page with the loopback intercepted.
 * @param authorization - The started authorization to decide.
 * @param button - The accessible name of the button to press.
 * @returns The query string of the URL the browser was sent to.
 */
async function decide(
	page: Page,
	authorization: Readonly<Authorization>,
	button: 'Allow' | 'Deny',
): Promise<URLSearchParams> {
	await page.goto(consentPath(authorization));
	const control = page.getByRole('button', { exact: true, name: button });
	await waitForHydration(control);
	await control.click();
	await page.waitForURL(LOOPBACK_PATTERN);
	return new URL(page.url()).searchParams;
}

/**
 * `./fixtures/auth` signs a user in only for a test that asks for one of its fixtures (`team`,
 * `teamSlug`, ...), and a test here that asked for just `page` would run signed out, reach the
 * login page and fail four assertions later for a reason that has nothing to do with the consent
 * card. This makes asking unnecessary: every test starts with a fresh signed-in user, and the one
 * that needs a signed-out visitor opens a context of its own.
 */
const test = authTest.extend<{ signedIn: undefined }>({
	signedIn: [
		async ({ team }, use) => {
			// Requested for its side effect (the user, its session cookies and its team); nothing here
			// reads the team itself.
			void team;
			await use(undefined);
		},
		{ auto: true },
	],
});

test.beforeAll(async () => {
	registeredClientId = await registerClient();
});

test.afterAll(async () => {
	const id = registeredClientId;
	registeredClientId = undefined;
	if (id !== undefined) await deleteClient(id);
});

test('shows the consent card and has no accessibility violations', async ({ page }) => {
	await page.goto(consentPath(await startAuthorization()));

	await expect(page.getByRole('heading', { level: 1, name: 'Allow CLI access' })).toBeVisible();
	await expect(
		page.getByText(`${CLIENT_NAME} wants to access kurze-url on your behalf.`),
	).toBeVisible();
	await expect(page.getByText(/^Signed in as e2e-.+@example\.com$/u)).toBeVisible();
	await expect(page.getByRole('button', { exact: true, name: 'Deny' })).toBeEnabled();
	await expect(page.getByRole('button', { exact: true, name: 'Allow' })).toBeEnabled();

	const results = await new AxeBuilder({ page }).analyze();
	expect(results.violations).toEqual([]);
});

test("Allow sends the code to the CLI's loopback URL", async ({ page }) => {
	const authorization = await startAuthorization();
	await interceptLoopback(page);

	const params = await decide(page, authorization, 'Allow');

	// The code is Supabase's to mint and opaque to us, so only its presence is asserted; the state
	// is ours, and the CLI refuses a callback whose state it did not send.
	expect(params.get('code')).toMatch(/\S/u);
	expect(params.get('state')).toBe(authorization.state);
	expect(params.get('error')).toBeNull();
});

test('Deny sends access_denied', async ({ page }) => {
	const authorization = await startAuthorization();
	await interceptLoopback(page);

	const params = await decide(page, authorization, 'Deny');

	expect(params.get('error')).toBe('access_denied');
	expect(params.get('state')).toBe(authorization.state);
	expect(params.get('code')).toBeNull();
});

test('a second authorization skips the card', async ({ page }) => {
	await interceptLoopback(page);
	await decide(page, await startAuthorization(), 'Allow');

	// The same user and client again. Supabase remembers the grant, so what it answers about the new
	// authorization carries only the redirect, and the page's loader follows that on the server: the
	// answer to the document request is itself the redirect, and there is no card to press anything
	// on. It is read as a request, not navigated to, because Playwright's `route` is not consulted
	// for the next hop of a redirect: the stub would not answer it, and a navigation would end in a
	// refused connection to a port nobody listens on.
	const second = await startAuthorization();
	const response = await page.request.get(consentPath(second), { maxRedirects: 0 });

	const { location } = response.headers();
	expect(location).toBeDefined();
	const target = new URL(location ?? '');
	expect(target.origin).toBe(LOOPBACK_ORIGIN);
	expect(target.pathname).toBe('/callback');
	expect(target.searchParams.get('code')).toMatch(/\S/u);
	expect(target.searchParams.get('state')).toBe(second.state);
});

test('a signed-out visitor goes to login with next', async ({ browser }, testInfo) => {
	const authorization = await startAuthorization();

	// `testInfo.project.use`, not a bare `browser.newContext()`: a manually created context does
	// not inherit `baseURL` or the Vercel protection-bypass header, and a protected preview would
	// answer with Vercel's own login redirect, which proves nothing about this page. The context
	// has no session cookies, which is the whole point. See `links.spec.ts`'s sign-out test.
	const context = await browser.newContext(testInfo.project.use);
	try {
		const page = await context.newPage();
		await page.goto(consentPath(authorization));

		await expect(page).toHaveURL(/\/login\?/u);
		// The consent URL, whole, as the search of `/login`: it is what the magic link's callback
		// reads back and follows once the visitor has signed in.
		expect(new URL(page.url()).searchParams.get('next')).toBe(consentPath(authorization));
	} finally {
		await context.close();
	}
});
