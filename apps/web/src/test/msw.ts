import { setupServer } from 'msw/node';

/**
 * Handlers are registered per test with `server.use(...)`. The base server
 * starts empty on purpose: a request no test asked for should fail loudly
 * rather than quietly hit a default handler someone forgot about.
 */
export const server = setupServer();
