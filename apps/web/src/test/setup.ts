import { afterAll, afterEach, beforeAll } from 'vitest';
// jest-dom's matcher augmentation is a side effect by design; there is nothing to assign.
// oxlint-disable-next-line import/no-unassigned-import
import '@testing-library/jest-dom/vitest';

import { server } from './msw';

// jsdom (this project's `unit` project) has never implemented `matchMedia` —
// nothing needed it until Task 4's `components/ui/sidebar.tsx` started
// calling it (via `hooks/use-mobile.ts`) to detect the mobile breakpoint.
// The `storybook` project needs no such stub: it runs in a real Chromium via
// Playwright, which implements `matchMedia` natively. `addEventListener`/
// `removeEventListener` are the only members `useIsMobile` actually calls;
// the rest of `MediaQueryList` is filled in only so the object shape matches.
if (typeof globalThis.matchMedia !== 'function') {
	// `addListener`/`removeListener` are deprecated in favour of
	// `addEventListener`/`removeEventListener` (which `useIsMobile` actually
	// calls) but still required by `MediaQueryList`'s own type — kept here only
	// to satisfy that shape, never called by anything this stub supports.
	// oxlint-disable typescript/no-deprecated
	globalThis.matchMedia = (query: string): MediaQueryList => ({
		addEventListener: () => undefined,
		addListener: () => undefined,
		dispatchEvent: () => false,
		matches: false,
		media: query,
		onchange: null,
		removeEventListener: () => undefined,
		removeListener: () => undefined,
	});
	// oxlint-enable typescript/no-deprecated
}

// onUnhandledRequest: 'error' is what makes the empty handler list above a
// feature rather than a gap — an unmocked call fails the test that made it.
beforeAll(() => {
	server.listen({ onUnhandledRequest: 'error' });
});
afterEach(() => {
	server.resetHandlers();
});

afterAll(() => {
	server.close();
});
