import { createFileRoute, redirect, useLoaderData, useNavigate } from '@tanstack/react-router';
import { useState } from 'react';

import {
	type ConsentAction,
	type ConsentCardState,
	OAuthConsentCard,
} from '../components/oauth-consent-card';
import { type ConsentDecision, decideConsent, loadConsent } from '../server/oauth-consent';

/** What the page does with an answer from the server, once it has judged it. */
type Outcome =
	| { readonly kind: 'follow'; readonly url: string }
	| { readonly kind: 'invalid' }
	| { readonly kind: 'login' };

/** Where the page is between a press of Allow or Deny and what follows it. */
type Phase =
	| { readonly kind: 'deciding' }
	| { readonly kind: 'failed'; readonly error: unknown }
	| { readonly kind: 'idle' }
	| { readonly kind: 'invalid' };

/** The consent member of the card's state: the one that has an authorization to decide. */
type ConsentRequest = Extract<ConsentCardState, { readonly kind: 'consent' }>;

interface ConsentDeciderProps {
	readonly consent: ConsentRequest;
}

const INVALID = { kind: 'invalid' } as const;
const DECIDING = { kind: 'deciding' } as const;
const IDLE = { kind: 'idle' } as const;

/**
 * Whether a URL is one the page may send the browser to. The server answers with the CLI's own
 * redirect (a loopback address, in practice), but what it sends is not this page's to vouch for,
 * and `javascript:` or `data:` in `Location` or `location.assign` is script running on this origin.
 * Only `http:` and `https:` are followed; a scheme-less or unparseable value is not a URL at all.
 *
 * @param url - What the server gave as a redirect.
 * @returns True when it parses as an `http:` or `https:` URL.
 */
function isHttpUrl(url: string): boolean {
	try {
		const { protocol } = new URL(url);
		return protocol === 'http:' || protocol === 'https:';
	} catch {
		return false;
	}
}

/**
 * The one place that decides what an answer from the server means for the page, for the load and
 * for a decision alike: a redirect is followed only when `isHttpUrl` allows it and is otherwise the
 * same as no usable authorization, and a signed-out answer goes to login.
 *
 * @param answer - What `loadConsent` answered, other than a consent to render, or what
 *   `decideConsent` answered.
 * @returns What to do about it.
 */
function outcomeOf(answer: ConsentDecision): Outcome {
	if (answer.kind === 'redirect') {
		return isHttpUrl(answer.url) ? { kind: 'follow', url: answer.url } : INVALID;
	}
	return answer.kind === 'unauthenticated' ? { kind: 'login' } : INVALID;
}

/**
 * The consent URL a signed-out visitor comes back to after signing in, as the search of `/login`.
 * `/auth/callback` follows a `next` only when `isSafeNext` accepts it, which is a path starting
 * `/oauth/consent?`; the id is percent-encoded so that nothing in it can end the query or add to it.
 *
 * @param authorizationId - The authorization the visitor was sent here for.
 * @returns The search params of `/login`.
 */
function loginSearch(authorizationId: string): { readonly next: string } {
	return { next: `/oauth/consent?authorization_id=${encodeURIComponent(authorizationId)}` };
}

/**
 * The route's loader. A missing or empty id is answered here as the error card, without a request:
 * there is nothing to ask about, and it is an ordinary thing to arrive with (an edited or
 * truncated link), so it neither throws nor reaches Sentry. Whether any other id is usable is the
 * server's to say, and it says `invalid` for the ones it cannot place.
 *
 * Redirects are thrown, not returned: a signed-out visitor goes to `/login` with `next` set to this
 * very URL, and an authorization already consented to goes straight back to the CLI, with `href`
 * because it is an absolute URL and `to` is a route pattern.
 *
 * @param authorizationId - The `authorization_id` search param, when the URL had a string for it.
 * @returns The state to render: the request, or the error card.
 */
export async function loadConsentPage(
	authorizationId: string | undefined,
): Promise<ConsentCardState> {
	if (authorizationId === undefined || authorizationId === '') return INVALID;
	const state = await loadConsent({ data: { authorizationId } });
	if (state.kind === 'consent') return state;
	const outcome = outcomeOf(state);
	if (outcome.kind === 'follow') {
		// oxlint-disable-next-line typescript/only-throw-error -- TanStack Router signals navigation by throwing; `redirect()` is its control flow, not an Error.
		throw redirect({ href: outcome.url });
	}
	if (outcome.kind === 'login') {
		// oxlint-disable-next-line typescript/only-throw-error -- same: the router's own control flow.
		throw redirect({ search: loginSearch(authorizationId), to: '/login' });
	}
	return INVALID;
}

/**
 * Sends one decision and does what the answer calls for. Resolves to the phase the page is then in:
 * still `deciding` when it is on its way out (to the CLI, or to `/login`), `invalid` when the
 * authorization turned out to be gone. A decision that throws is not caught here.
 *
 * @param action - Which button was pressed.
 * @param authorizationId - The authorization being decided.
 * @param navigate - The router's navigation, for the redirect to `/login`.
 * @returns The phase to show once the answer has been acted on.
 */
async function settleDecision(
	action: ConsentAction,
	authorizationId: string,
	navigate: ReturnType<typeof useNavigate>,
): Promise<Phase> {
	const outcome = outcomeOf(await decideConsent({ data: { action, authorizationId } }));
	if (outcome.kind === 'follow') {
		globalThis.location.assign(outcome.url);
		return DECIDING;
	}
	if (outcome.kind === 'login') {
		await navigate({ search: loginSearch(authorizationId), to: '/login' });
		return DECIDING;
	}
	return INVALID;
}

/**
 * The request on screen, and what pressing Allow or Deny does. `deciding` is state set in the click
 * handler itself, so the re-render that disables both buttons is part of the same event and a
 * second click finds them already disabled (Review Focus 3). It is never reset after a decision
 * that is followed: the page is leaving, and re-enabling the buttons for that moment would offer a
 * second decision on a spent authorization.
 *
 * A decision that throws (the server or the network failed) is a real failure and not a state to
 * render: it is rethrown during render, which is how an error from an event handler reaches the
 * route's error boundary, where the app reports unexpected failures and shows its generic message.
 * A reload asks the server again.
 *
 * An `invalid` phase swaps the request for the error card under the same mounted card, and the card
 * itself moves focus to its heading when that happens (the focused button was disabled by
 * `deciding`, so focus would otherwise be lost and the change never announced).
 *
 * @param props - The component's props.
 * @param props.consent - The request the server confirmed for this signed-in person.
 * @returns The consent card, or the error card once the authorization has proved to be gone.
 */
function ConsentDecider({ consent }: ConsentDeciderProps): React.JSX.Element {
	const navigate = useNavigate();
	const [phase, setPhase] = useState<Phase>(IDLE);
	// oxlint-disable-next-line typescript/only-throw-error -- whatever the failed call rejected with, handed on unchanged to the error boundary, which takes `unknown`.
	if (phase.kind === 'failed') throw phase.error;

	return (
		<OAuthConsentCard
			deciding={phase.kind === 'deciding'}
			onDecide={(action) => {
				setPhase(DECIDING);
				void (async () => {
					try {
						setPhase(await settleDecision(action, consent.authorizationId, navigate));
					} catch (error) {
						setPhase({ error, kind: 'failed' });
					}
				})();
			}}
			state={phase.kind === 'invalid' ? INVALID : consent}
		/>
	);
}

/** The error card has no buttons, so nothing can call this. */
function ignoreDecision(): void {
	// Nothing to decide.
}

/**
 * The route's component: the consent card for a confirmed request, the error card otherwise.
 *
 * @returns The page for what the loader found.
 */
function ConsentPage(): React.JSX.Element {
	const loaded = useLoaderData({ from: '/oauth/consent' });
	return loaded.kind === 'consent' ? (
		<ConsentDecider consent={loaded} />
	) : (
		<OAuthConsentCard deciding={false} onDecide={ignoreDecision} state={loaded} />
	);
}

/**
 * Only a string can be an authorization id. TanStack parses numbers and JSON literals out of a
 * query string, and what is not a string is dropped here, which the loader answers as the error
 * card; whether a string is a real authorization is Supabase's to say, through the server.
 *
 * @param search - The parsed search string.
 * @returns The search params this route understands.
 */
function validateConsentSearch(search: Readonly<Record<string, unknown>>): {
	readonly authorization_id?: string;
} {
	return {
		authorization_id:
			typeof search.authorization_id === 'string' ? search.authorization_id : undefined,
	};
}

// oxlint-disable-next-line sort-keys -- `validateSearch` has to be declared before `loaderDeps` and `loader`: `loaderDeps`'s `search` and the loader's `deps` are typed from what comes before them, and declaring `loaderDeps` and `loader` first made `deps` fall back to `{}`.
export const Route = createFileRoute('/oauth/consent')({
	validateSearch: validateConsentSearch,
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- TanStack Router's own option shape, not a declaration this file can edit.
	loaderDeps: ({ search }) => ({ authorizationId: search.authorization_id }),
	// oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same: the router's own option shape.
	loader: async ({ deps }) => loadConsentPage(deps.authorizationId),
	component: ConsentPage,
});
