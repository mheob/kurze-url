/**
 * Reads and validates the three environment variables authenticated e2e needs, throwing one
 * combined error naming every missing one — without them these specs would run signed out and
 * pass against the login page, the same failure the protection-bypass work fixed in September.
 *
 * Its own module rather than a member of `./auth`, because `./seed` needs the same
 * `E2E_DATABASE_URL` for its own connection and reading it in two places would let the two drift.
 * The point of the combined error is that a run missing several variables says so once instead of
 * failing three times in a row.
 *
 * @returns The validated `SUPABASE_URL`, `SUPABASE_SERVICE_ROLE_KEY` and `E2E_DATABASE_URL`.
 */
export function requireE2eEnv(): { databaseUrl: string; serviceRoleKey: string; url: string } {
	const url = process.env.SUPABASE_URL;
	const serviceRoleKey = process.env.SUPABASE_SERVICE_ROLE_KEY;
	const databaseUrl = process.env.E2E_DATABASE_URL;
	if (
		url === undefined ||
		url === '' ||
		serviceRoleKey === undefined ||
		serviceRoleKey === '' ||
		databaseUrl === undefined ||
		databaseUrl === ''
	) {
		const missing: string[] = [];
		if (url === undefined || url === '') missing.push('SUPABASE_URL');
		if (serviceRoleKey === undefined || serviceRoleKey === '')
			missing.push('SUPABASE_SERVICE_ROLE_KEY');
		if (databaseUrl === undefined || databaseUrl === '') missing.push('E2E_DATABASE_URL');
		throw new Error(
			`${missing.join(', ')} ${missing.length === 1 ? 'is' : 'are'} required for authenticated e2e. ` +
				'Without them these specs would run signed out and pass against the login page — ' +
				'the same failure the protection-bypass work fixed in September.',
		);
	}
	return { databaseUrl, serviceRoleKey, url };
}
