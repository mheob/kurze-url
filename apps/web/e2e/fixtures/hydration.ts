import { expect, type Locator } from '@playwright/test';

/**
 * Waits until React has hydrated a specific element, which `page.goto` does
 * not: it resolves on the `load` event, and this app server-renders, so a form
 * is on screen and fillable well before React takes it over.
 *
 * Playwright is fast enough for that gap to matter, and what happens in it is
 * quiet. `fill` writes the value into the DOM and dispatches an `input` event
 * that no React listener is attached to yet, so React's own state stays empty
 * while the field looks filled. Nothing is visibly wrong until the *first*
 * re-render — clicking submit is one — at which point React writes its own
 * (empty) state back over the DOM value and the form submits blank. Empty
 * fails validation, so no request is ever made, and the test then times out
 * waiting for a result while looking at a form whose field has gone empty
 * again.
 *
 * Reproduced against a local production build by delaying `/assets/*.js` by
 * 2.5s: filling before hydration leaves the field empty after submit and
 * creates nothing, filling after it round-trips normally. In CI it hit roughly
 * one run in three, on whichever test happened to lose the race — a trace of
 * one has `fill` starting 30 ms after `goto` returned.
 *
 * The signal is React's own: it stamps a `__reactFiber$…` property onto each
 * DOM node as it hydrates that node, so once the property is there React owns
 * the element and an `input` event will reach its state. It is an internal
 * name, and the alternative was retrying `fill` + `click` until something
 * stuck, which creates duplicate rows whenever the click did land and only the
 * assertion was slow. This waits for the actual condition instead.
 */
export async function waitForHydration(locator: Locator): Promise<void> {
	await expect
		.poll(async () =>
			locator.evaluate((node) => Object.keys(node).some((key) => key.startsWith('__reactFiber$'))),
		)
		.toBe(true);
}
