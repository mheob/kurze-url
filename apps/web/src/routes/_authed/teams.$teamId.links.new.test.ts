import { describe, expect, it, vi } from 'vitest';

import { afterCreate } from './teams.$teamId.links.new';

/**
 * This task's own explicit rule: "After a successful create, invalidate
 * both the links query key and the router... invalidating only one leaves
 * them disagreeing until the next full navigation." Falsified against
 * hand-built fakes — the same reasoning `loadLinks`'s own test file (Task 9)
 * gives for testing against a narrow interface instead of a real
 * `QueryClient`/router pair.
 *
 * Named `....new.test.ts`, not `....new.test.tsx`: this file has no JSX, so
 * there's no basename collision to worry about (conventions.md's landmine is
 * specifically a same-basename `.test.ts`/`.test.tsx` pair).
 */
describe('afterCreate', () => {
	it('invalidates both the links query cache and the router', async () => {
		const invalidateQueries = vi.fn(async (): Promise<void> => undefined);
		const invalidate = vi.fn(async (): Promise<void> => undefined);

		await afterCreate({ invalidateQueries }, { invalidate }, 'team-a');

		expect(invalidateQueries).toHaveBeenCalledExactlyOnceWith({ queryKey: ['links', 'team-a'] });
		expect(invalidate).toHaveBeenCalledTimes(1);
	});
});
