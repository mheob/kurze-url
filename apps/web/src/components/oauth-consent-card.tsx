import { ShieldAlertIcon } from 'lucide-react';
import { useEffect, useRef } from 'react';
import { useTranslation } from 'react-i18next';

import type { ConsentState } from '../server/oauth-consent';
import { Button } from './ui/button';
import { Card, CardContent, CardHeader, CardTitle } from './ui/card';

/** The two things a person can answer to a consent request. */
export type ConsentAction = 'approve' | 'deny';

/**
 * What the card can show: the request itself, or the explanation for a request that cannot be
 * completed. The other two states a loaded authorization can be in (`redirect`, `unauthenticated`)
 * are followed by the route before anything renders, so the card never sees them.
 */
export type ConsentCardState = Extract<ConsentState, { readonly kind: 'consent' | 'invalid' }>;

export interface OAuthConsentCardProps {
	/**
	 * True from the moment a decision is sent until the page leaves. Both buttons are disabled for
	 * all of it, which is what makes a double click on Allow send one decision rather than two.
	 */
	readonly deciding: boolean;
	/** Called with the person's answer. Not called while `deciding`, or from the invalid state. */
	readonly onDecide: (action: ConsentAction) => void;
	readonly state: ConsentCardState;
}

/**
 * The page a CLI login sends the person to: who is asking, what they would be handing over, and
 * Allow or Deny. It lays itself out like `LoginForm` (its own `<main>`, a centred narrow card, the
 * page's only `<h1>` inside `CardTitle`) because it is a standalone page of the same kind, outside
 * the authenticated shell.
 *
 * The warning is a `role="note"` block and not an alert: it is part of the page's content on every
 * visit, so announcing it as news would be wrong; a screen reader reads it in place, between the
 * request and the buttons. It draws on the destructive tokens `LinkScanNotice` uses, so no new
 * colour exists to keep in contrast. Deny comes first and Allow last, right-aligned, so the
 * primary action sits where a dialog's confirmation does and the default tab order reads "refuse,
 * then accept".
 *
 * Pressing Allow or Deny disables the button that has focus, and an authorization that then proves
 * to be gone turns this very card into the explanation: focus would fall to `<body>` and nothing
 * would announce the change (WCAG 2.4.3, 4.1.3). So when `state` becomes `invalid` under a card
 * that was showing a request, focus moves to the heading, which now names the new state; it takes
 * `tabIndex={-1}` to be focusable without joining the tab order. A card that is `invalid` from its
 * first render leaves focus where the browser put it: a page that loads does not take focus, and
 * nothing has been decided yet.
 *
 * @param props - The card's props.
 * @param props.deciding - Whether a decision is in flight, which disables both buttons.
 * @param props.onDecide - Called with `approve` or `deny` when a button is pressed.
 * @param props.state - The request to show, or the explanation that it cannot be completed.
 * @returns The card, inside a `<main>` landmark.
 */
export function OAuthConsentCard({
	deciding,
	onDecide,
	state,
}: OAuthConsentCardProps): React.JSX.Element {
	const { t } = useTranslation();
	const heading = useRef<HTMLHeadingElement>(null);
	const previousKind = useRef(state.kind);

	useEffect(() => {
		if (state.kind === 'invalid' && previousKind.current === 'consent') heading.current?.focus();
		previousKind.current = state.kind;
	}, [state.kind]);

	return (
		<main className="flex min-h-screen items-center justify-center bg-background px-6 py-12 text-foreground">
			<div className="w-full max-w-sm">
				<Card>
					<CardHeader>
						{/* `CardTitle` hardcodes a `<div>`, so the page's only heading is a real
						    `<h1>` inside it, as `login.tsx` does for the same reason. */}
						<CardTitle>
							<h1 ref={heading} tabIndex={-1}>
								{state.kind === 'consent'
									? t('oauthConsent.title')
									: t('oauthConsent.invalidTitle')}
							</h1>
						</CardTitle>
					</CardHeader>
					<CardContent>
						{/* A plain `<div>` for the stack: `react/forbid-component-props` keeps
						    `className` off `CardContent`, and the card's own padding is what it is for. */}
						<div className="flex flex-col gap-4">
							{state.kind === 'consent' ? (
								<>
									<p>{t('oauthConsent.request', { client: state.clientName })}</p>
									<p>{t('oauthConsent.scope')}</p>
									<div
										className="flex items-start gap-2 border border-destructive/50 bg-destructive/10 p-3 text-sm text-foreground [&_svg]:mt-0.5 [&_svg]:shrink-0"
										role="note"
									>
										<ShieldAlertIcon aria-hidden />
										<p>{t('oauthConsent.warning')}</p>
									</div>
									<p className="text-sm text-muted-foreground">
										{t('oauthConsent.signedInAs', { email: state.email })}
									</p>
									<div className="flex justify-end gap-2">
										<Button
											disabled={deciding}
											onClick={() => {
												onDecide('deny');
											}}
											type="button"
											variant="outline"
										>
											{t('oauthConsent.deny')}
										</Button>
										<Button
											disabled={deciding}
											onClick={() => {
												onDecide('approve');
											}}
											type="button"
										>
											{t('oauthConsent.allow')}
										</Button>
									</div>
								</>
							) : (
								<p>{t('oauthConsent.invalidBody')}</p>
							)}
						</div>
					</CardContent>
				</Card>
			</div>
		</main>
	);
}
