// jest-dom's matcher augmentation is a side effect by design; there is nothing to assign.
// oxlint-disable-next-line import/no-unassigned-import
import '@testing-library/jest-dom/vitest';
import { afterAll, afterEach, beforeAll } from 'vitest';

import { server } from './msw';

// onUnhandledRequest: 'error' is what makes the empty handler list above a
// feature rather than a gap — an unmocked call fails the test that made it.
beforeAll(() => server.listen({ onUnhandledRequest: 'error' }));
afterEach(() => server.resetHandlers());
afterAll(() => server.close());
