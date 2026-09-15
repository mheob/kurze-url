// Run the whole local stack with one command: Postgres and auth (Supabase),
// Redis, the Go API, and the web app.
//
// Written in TypeScript rather than shell for two reasons. Node 24 runs a .ts
// file directly through type stripping, so there is no build step; and unlike
// `scripts/restore-local.sh`, which nothing checks, this file is covered by
// `pnpm lint` and `pnpm typecheck`.
//
// Two things it deliberately does NOT do, both because they destroy state a
// developer wants to keep between runs:
//
//   - It never stops Supabase or the Redis container on exit. A second
//     `pnpm dev` then starts in seconds instead of a minute, and the local
//     database survives. `pnpm dev:stop` shuts both down.
//   - It never runs `supabase db reset`. That drops the local database, which
//     would silently delete every team and link created while developing.
//     `pnpm db:reset` is the explicit way to ask for it.

/// <reference types="node" />

import type { ChildProcess } from 'node:child_process';
import type { Readable } from 'node:stream';

import { spawn, spawnSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import { connect } from 'node:net';
import { createInterface } from 'node:readline';
import { parseEnv } from 'node:util';

// `parseEnv` widens every value to `string | undefined` under the root
// tsconfig's noUncheckedIndexedAccess, and that is also exactly the shape
// `spawn`'s `env` option takes — so the parsed file passes straight through.
type EnvPairs = Readonly<Record<string, string | undefined>>;

const REDIS_CONTAINER = 'kurze-url-redis';
const REDIS_IMAGE = 'redis:7-alpine';
const POSTGRES_PORT = 54_322;
const REDIS_PORT = 6379;
const API_HEALTH_URL = 'http://127.0.0.1:8080/health';
const API_ENV_FILE = 'apps/api/.env';
const WEB_ENV_FILE = 'apps/web/.env.local';
const SIGNING_KEYS_FILE = 'supabase/signing_keys.json';
const SUPABASE_CONFIG_FILE = 'supabase/config.toml';
const SIGNING_KEYS_REMEDY = `printf '[]' > ${SIGNING_KEYS_FILE} && supabase gen signing-key --algorithm ES256 --append`;

const PROBE_TIMEOUT_MS = 1000;
const PROBE_INTERVAL_MS = 250;
const MS_PER_SECOND = 1000;
const POSTGRES_READY_TIMEOUT_MS = 60_000;
const REDIS_READY_TIMEOUT_MS = 30_000;
const API_READY_TIMEOUT_MS = 60_000;

/**
 * Every preflight failure is one problem plus the command that fixes it. They
 * travel as a single formatted message rather than a custom Error subclass so
 * the top-level catch needs no type test to print them usefully.
 *
 * @param problem - What is wrong, in one line.
 * @param remedy - The command or edit that fixes it.
 * @returns The error to throw.
 */
function preflightError(problem: string, remedy: string): Error {
	return new Error(`${problem}\n\nfix: ${remedy}`);
}

/**
 * @param command - Executable to run.
 * @param args - Arguments to pass.
 * @returns Whether the command was found and exited 0. Output is discarded.
 */
function succeeds(command: string, args: readonly string[]): boolean {
	const result = spawnSync(command, [...args], { stdio: 'ignore' });
	return result.error === undefined && result.status === 0;
}

/**
 * Reads a dotenv file into a plain object. Used for both env files — the Go
 * API has no dotenv dependency of its own, so its values have to be handed to
 * it as the child's environment.
 *
 * @param path - Path to the dotenv file, relative to the repository root.
 * @returns The parsed key/value pairs, or an empty object if the file is absent.
 */
function readEnvFile(path: string): EnvPairs {
	if (!existsSync(path)) return {};
	return parseEnv(readFileSync(path, 'utf8'));
}

/**
 * The origin both halves of the stack must agree on. A mismatch here is the
 * failure this check exists for: the web app signs a visitor in against one
 * Supabase project while the API verifies tokens against another, so login
 * appears to work and every `/v1` call answers 401.
 *
 * @param value - A Supabase URL from either env file.
 * @returns Its origin, or `undefined` if the value is missing or unparseable.
 */
function originOf(value: string | undefined): string | undefined {
	if (value === undefined || value === '') return undefined;
	try {
		return new URL(value).origin;
	} catch {
		return undefined;
	}
}

/**
 * @param key - One entry of the parsed signing keys file.
 * @returns Whether it declares the ES256 algorithm the API requires.
 */
function isES256(key: unknown): boolean {
	return typeof key === 'object' && key !== null && 'alg' in key && key.alg === 'ES256';
}

/**
 * @param path - File to read and parse.
 * @param remedy - Included in the thrown error when the contents are not JSON.
 * @returns The parsed value.
 * @throws {Error} When the file does not hold valid JSON.
 */
function parseJsonFile(path: string, remedy: string): unknown {
	try {
		return JSON.parse(readFileSync(path, 'utf8'));
	} catch {
		throw preflightError(`${path} is not valid JSON`, remedy);
	}
}

/**
 * The API rejects the legacy HS256 shared secret and verifies ES256 through
 * JWKS, so a local stack without an asymmetric signing key issues tokens it
 * can never accept — visible only as a 401 after a login that looked fine.
 *
 * The file has to be a JSON *array*; the Supabase CLI prints a single object
 * and reads the configured path before generating anything, which is why the
 * working recipe is `printf '[]' > …` followed by `--append` rather than a
 * shell redirect into the file itself.
 *
 * @throws {Error} When the key is missing, malformed, or not ES256.
 */
function checkSigningKeys(): void {
	const config = existsSync(SUPABASE_CONFIG_FILE) ? readFileSync(SUPABASE_CONFIG_FILE, 'utf8') : '';
	if (!/^\s*signing_keys_path\s*=/mu.test(config)) {
		throw preflightError(
			`${SUPABASE_CONFIG_FILE} has no active signing_keys_path, so the local stack signs HS256 and the API rejects every token`,
			`uncomment signing_keys_path in ${SUPABASE_CONFIG_FILE}, then: ${SIGNING_KEYS_REMEDY}`,
		);
	}
	if (!existsSync(SIGNING_KEYS_FILE)) {
		throw preflightError(`${SIGNING_KEYS_FILE} is missing`, SIGNING_KEYS_REMEDY);
	}
	const keys: unknown = parseJsonFile(SIGNING_KEYS_FILE, SIGNING_KEYS_REMEDY);
	if (!Array.isArray(keys) || !keys.some((key) => isES256(key))) {
		throw preflightError(
			`${SIGNING_KEYS_FILE} must be a JSON array holding at least one ES256 key`,
			SIGNING_KEYS_REMEDY,
		);
	}
}

/**
 * The three external programs the stack is built out of.
 *
 * @throws {Error} When one of them is missing or not answering.
 */
function checkTools(): void {
	if (!succeeds('docker', ['info'])) {
		throw preflightError(
			'the Docker daemon is not reachable',
			'start Docker Desktop, or install it from https://docs.docker.com/get-docker/',
		);
	}
	if (!succeeds('supabase', ['--version'])) {
		throw preflightError('the Supabase CLI is not on PATH', 'brew install supabase/tap/supabase');
	}
	if (!succeeds('go', ['version'])) {
		throw preflightError('Go is not on PATH', 'install Go 1.27 or newer');
	}
}

/**
 * @returns The parsed web env file, once both values it must carry are present.
 * @throws {Error} When either env file is missing a value the stack needs.
 */
function checkEnvFiles(): EnvPairs {
	if (!existsSync(API_ENV_FILE)) {
		throw preflightError(
			`${API_ENV_FILE} is missing`,
			`cp apps/api/.env.example ${API_ENV_FILE} — then set VISITOR_SALT and the local Supabase values`,
		);
	}
	const webEnv = readEnvFile(WEB_ENV_FILE);
	for (const key of ['SUPABASE_URL', 'SUPABASE_PUBLISHABLE_KEY']) {
		const value = webEnv[key];
		if (value === undefined || value === '') {
			throw preflightError(
				`${key} is not set in ${WEB_ENV_FILE}`,
				'see apps/web/.env.example — for a local stack, take both values from `supabase status`',
			);
		}
	}
	return webEnv;
}

/**
 * Compared as origins rather than as strings: the two variables are different
 * URLs on the same host by design — one is the project URL, the other a JWKS
 * path beneath it — so only host and port can be compared.
 *
 * @param webEnv - The already-parsed web env file.
 * @throws {Error} When the two halves of the stack trust different Supabase instances.
 */
function checkSupabaseOriginsAgree(webEnv: EnvPairs): void {
	const webOrigin = originOf(webEnv.SUPABASE_URL);
	const apiOrigin = originOf(readEnvFile(API_ENV_FILE).SUPABASE_JWKS_URL);
	if (webOrigin !== undefined && apiOrigin !== undefined && webOrigin !== apiOrigin) {
		throw preflightError(
			`the web app signs in against ${webOrigin} but the API verifies tokens from ${apiOrigin}`,
			`point SUPABASE_JWKS_URL and SUPABASE_JWT_ISSUER in ${API_ENV_FILE} at ${webOrigin}`,
		);
	}
}

/**
 * Everything that can be established without starting anything. Ordered so the
 * cheapest and most fundamental failures report first.
 *
 * @throws {Error} On the first problem found.
 */
function preflight(): void {
	checkTools();
	const webEnv = checkEnvFiles();
	checkSigningKeys();
	checkSupabaseOriginsAgree(webEnv);
}

/**
 * @param port - TCP port on 127.0.0.1.
 * @returns Whether something is accepting connections there.
 */
async function isListening(port: number): Promise<boolean> {
	return new Promise((resolve) => {
		const socket = connect({ host: '127.0.0.1', port });
		const settle = (answer: boolean): void => {
			socket.destroy();
			resolve(answer);
		};
		socket.setTimeout(PROBE_TIMEOUT_MS);
		socket.once('connect', () => {
			settle(true);
		});
		socket.once('timeout', () => {
			settle(false);
		});
		socket.once('error', () => {
			settle(false);
		});
	});
}

/**
 * @returns Whether the API answers its unauthenticated health route.
 */
async function isApiHealthy(): Promise<boolean> {
	try {
		const response = await fetch(API_HEALTH_URL, { signal: AbortSignal.timeout(PROBE_TIMEOUT_MS) });
		return response.ok;
	} catch {
		return false;
	}
}

/**
 * @param milliseconds - How long to pause.
 * @returns A promise that settles once the pause is over.
 */
async function sleep(milliseconds: number): Promise<void> {
	return new Promise((resolve) => {
		setTimeout(resolve, milliseconds);
	});
}

/**
 * @param label - What is being waited for, used in the failure message.
 * @param probe - Resolves true once the thing is ready.
 * @param timeoutMs - How long to keep trying before giving up.
 * @throws {Error} When the probe never succeeds inside the timeout.
 */
async function waitUntil(
	label: string,
	probe: () => Promise<boolean>,
	timeoutMs: number,
): Promise<void> {
	const deadline = Date.now() + timeoutMs;
	while (Date.now() < deadline) {
		if (await probe()) return;
		await sleep(PROBE_INTERVAL_MS);
	}
	throw new Error(`${label} did not become ready within ${timeoutMs / MS_PER_SECOND}s`);
}

/**
 * Starts the Supabase stack unless it is already up. `supabase status` exits
 * non-zero when it is not running, which is the cheapest way to ask.
 *
 * @throws {Error} When the stack fails to start.
 */
function startSupabase(): void {
	if (succeeds('supabase', ['status'])) {
		console.log('supabase | already running');
		return;
	}
	console.log('supabase | starting, this takes a moment on the first run');
	if (spawnSync('supabase', ['start'], { stdio: 'inherit' }).status !== 0) {
		throw new Error('supabase start failed');
	}
}

/**
 * Reuses the named container if it exists, so the image is pulled once and
 * `docker run` never fails with a name conflict on the second run.
 *
 * @throws {Error} When neither starting nor creating the container works.
 */
function startRedis(): void {
	if (succeeds('docker', ['start', REDIS_CONTAINER])) {
		console.log(`redis    | container ${REDIS_CONTAINER} started`);
		return;
	}
	console.log(`redis    | creating container ${REDIS_CONTAINER} from ${REDIS_IMAGE}`);
	const args = ['run', '-d', '--name', REDIS_CONTAINER, '-p', `${REDIS_PORT}:6379`, REDIS_IMAGE];
	if (spawnSync('docker', args, { stdio: 'inherit' }).status !== 0) {
		throw new Error('could not start the Redis container');
	}
}

/**
 * @param stream - One of a child's output streams, or null when it was not piped.
 * @param prefix - Written before each line.
 */
/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same cause as
 * `relayOutput` above.
 */
function relayStream(stream: Readable | null, prefix: string): void {
	if (stream === null) return;
	createInterface({ input: stream }).on('line', (line: string) => {
		console.log(`${prefix} | ${line}`);
	});
}

/**
 * Prefixes every line of a child's output so two interleaved servers stay
 * readable. Line-buffered rather than chunk-buffered, or a prefix would land
 * in the middle of a line.
 *
 * @param child - The spawned process whose stdout and stderr to relay.
 * @param prefix - Written before each line, already padded to a fixed width.
 */
/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- `ChildProcess`
 * nests mutable streams and an EventEmitter that a shallow `Readonly<>` cannot reach;
 * same cause as the `Request` parameters in apps/web/src/server/supabase.ts.
 */
function relayOutput(child: ChildProcess, prefix: string): void {
	relayStream(child.stdout, prefix);
	relayStream(child.stderr, prefix);
}

/**
 * Spawned into its own process group on purpose. `go run` compiles to a
 * temporary binary and execs it as a child, so signalling the `go run` process
 * alone leaves that binary running and port 8080 occupied — the next
 * `pnpm dev` then fails for a reason that looks nothing like its cause.
 * Killing the group reaches both. The web child is treated the same way,
 * because Vite spawns workers of its own.
 *
 * @param command - Executable to run.
 * @param args - Arguments to pass.
 * @param options - Working directory, extra environment, and the output prefix.
 * @returns The spawned child.
 */
function spawnServer(
	command: string,
	args: readonly string[],
	options: Readonly<{ cwd: string; env?: EnvPairs; prefix: string }>,
): ChildProcess {
	const child = spawn(command, [...args], {
		cwd: options.cwd,
		detached: true,
		env: { ...process.env, ...options.env },
		stdio: ['ignore', 'pipe', 'pipe'],
	});
	relayOutput(child, options.prefix);
	return child;
}

/**
 * Signals a child's whole process group, ignoring the error raised when it has
 * already exited.
 *
 * @param child - The child to stop.
 */
/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- same cause as
 * `relayOutput` above.
 */
function stopServer(child: ChildProcess): void {
	if (child.pid === undefined || child.exitCode !== null) return;
	try {
		process.kill(-child.pid, 'SIGTERM');
	} catch {
		// Already gone; nothing left to signal.
	}
}

/**
 * Wires Ctrl-C and SIGTERM to stop the servers this script started, and
 * returns the same handler so an unexpected child exit can take the other
 * child down through it. Idempotent: the second call does nothing, which
 * matters because both paths can fire for one shutdown.
 *
 * @param children - The server processes to stop; read at signal time, so it may still be empty.
 * @returns The shutdown handler.
 */
/* oxlint-disable-next-line typescript/prefer-readonly-parameter-types -- the array is
 * readonly; its `ChildProcess` elements are not, for the same reason `relayStream`
 * above carries this disable.
 */
function onInterrupt(children: readonly ChildProcess[]): () => void {
	let stopping = false;
	const shutdown = (): void => {
		if (stopping) return;
		stopping = true;
		console.log('\ndev      | stopping API and web — Supabase and Redis stay up (pnpm dev:stop)');
		for (const child of children) stopServer(child);
	};
	process.on('SIGINT', shutdown);
	process.on('SIGTERM', shutdown);
	return shutdown;
}

/**
 * Starts everything the API depends on and waits until it can actually be
 * reached — a port that is merely bound is not a database that answers.
 */
async function startInfrastructure(): Promise<void> {
	startSupabase();
	startRedis();
	await waitUntil('Postgres', async () => isListening(POSTGRES_PORT), POSTGRES_READY_TIMEOUT_MS);
	await waitUntil('Redis', async () => isListening(REDIS_PORT), REDIS_READY_TIMEOUT_MS);
}

/**
 * Brings the whole stack up, then stays in the foreground relaying output
 * until interrupted or until one of the two servers exits on its own.
 */
async function main(): Promise<void> {
	preflight();
	await startInfrastructure();

	const children: ChildProcess[] = [];
	const shutdown = onInterrupt(children);

	children.push(
		spawnServer('go', ['run', './cmd/api'], {
			cwd: 'apps/api',
			// The Go API reads os.Getenv and has no dotenv dependency. Passing the
			// parsed file to this child alone is deliberate: loading it into this
			// process would hand DATABASE_URL and VISITOR_SALT to the web child too.
			env: readEnvFile(API_ENV_FILE),
			prefix: 'api     ',
		}),
	);
	await waitUntil('the API', isApiHealthy, API_READY_TIMEOUT_MS);
	console.log('api      | healthy on http://127.0.0.1:8080');

	children.push(
		spawnServer('pnpm', ['--filter', '@kurze-url/web', 'dev'], { cwd: '.', prefix: 'web     ' }),
	);

	// Whichever server exits first takes the other with it: a dev stack missing
	// half of itself produces errors about the wrong thing.
	process.exitCode = await new Promise<number>((resolve) => {
		for (const child of children) {
			child.once('exit', (code) => {
				shutdown();
				resolve(code ?? 0);
			});
		}
	});
}

try {
	await main();
} catch (error) {
	console.error(`\ndev: ${error instanceof Error ? error.message : String(error)}\n`);
	process.exitCode = 1;
}
