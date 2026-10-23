// Measures how Supabase's OAuth 2.1 server behaves for the CLI, against the local stack only.
//
// The CLI spec (docs/superpowers/specs/2026-10-23-cli-design.md, Part 1) names three risks
// that Supabase's documentation leaves open: whether loopback redirect URIs on other ports or
// hostnames are accepted, which audience the server's access tokens carry, and how refresh
// rotation, the sign-out scopes and a revoked grant reach a CLI session. This script drives a
// complete public-client flow over plain HTTP and prints what it observed as one JSON object:
// it registers a client, authorizes with PKCE, approves the consent as a signed-in user,
// exchanges the code, refreshes, and signs out in each scope. It also sends the OAuth calls
// once without the `apikey` header, the way a browser and a plain OAuth client send them, to
// show whether the CLI needs the publishable key. The spec's "Verified" section is written
// from that output, and re-running it is how a later Supabase release gets re-checked.
//
// Run it with the local stack up (`supabase start`): `pnpm probe:oauth`. It reads the stack's
// URL and keys from `supabase status -o json` and refuses any API URL, and any endpoint the
// discovery document names, whose host is not 127.0.0.1 or localhost — before the first
// request for the former, before any credential leaves for the latter. `--api-url` exists to
// prove that refusal. No key, token, authorization code or PKCE verifier is ever printed; the
// report carries lengths, claim names and claim values that are not credentials. The user and
// the client it creates are deleted again even when a step fails (`--fail-after-client` forces
// one, by sending a wrong PKCE verifier to the token endpoint), and a failed delete makes the
// run exit 1.

import { execFileSync } from 'node:child_process';
import { createHash, randomBytes, randomUUID } from 'node:crypto';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { setTimeout as sleep } from 'node:timers/promises';

const CLIENT_NAME = 'kurze-url CLI';
const REGISTERED_REDIRECT = 'http://127.0.0.1:53682/callback';
const OTHER_PORT_REDIRECT = 'http://127.0.0.1:53999/callback';
const LOCALHOST_REDIRECT = 'http://localhost:53682/callback';
const CONSENT_PATH = '/oauth/consent';
const DISCOVERY_PATH = '/auth/v1/.well-known/oauth-authorization-server';
const WINDOWS_CREDENTIAL_LIMIT_BYTES = 2560;
const LOCAL_HOSTS = new Set(['127.0.0.1', 'localhost']);
const HTTP_SUCCESS_FIRST = 200;
const HTTP_SUCCESS_LAST = 299;
const VERIFIER_BYTES = 32;
const PASSWORD_BYTES = 24;
const MILLISECONDS_PER_SECOND = 1000;
// The local default (supabase/config.toml); used only when the line cannot be read.
const DEFAULT_REUSE_INTERVAL_SECONDS = 10;
// Past the interval, so the reuse is judged by the rule and not by the clock's rounding.
const REUSE_MARGIN_SECONDS = 2;
const UUID_PATTERN = /^[\da-f]{8}(?:-[\da-f]{4}){3}-[\da-f]{12}$/u;
const CONFIG_PATTERNS = {
	reuseInterval: /^refresh_token_reuse_interval\s*=\s*(?<value>\d+)/mu,
	siteUrl: /^site_url\s*=\s*"(?<value>[^"]+)"/mu,
} as const;

type Headers = Readonly<Record<string, string>>;
type Json = Readonly<Record<string, unknown>>;
type Reply = Readonly<{ body: Json; location: string | undefined; status: number }>;
type Tokens = Readonly<{ access: string; refresh: string }>;
type Outcome = Readonly<{ reason: string | null; status: number }>;
type Reuse = Readonly<{ returnsActive: boolean; status: number }>;
type Chain = Readonly<{ access: string; chain: readonly [string, string, string] }>;
type Redirect = Readonly<{
	error: string | null;
	locationOrigin: string | null;
	locationPath: string | null;
	status: number;
}>;
type Request = Readonly<{
	form?: Headers;
	headers: Headers;
	json?: Json;
	method: string;
	url: string;
}>;
type Stack = Readonly<{ anonKey: string; apiUrl: string; serviceRoleKey: string }>;
type Probe = Readonly<{
	admin: Headers;
	anon: Headers;
	anonKey: string;
	auth: string;
	authorizationEndpoint: string;
	clientId: string;
	codeChallengeMethods: readonly string[];
	discoveryUrl: string;
	email: string;
	password: string;
	tokenEndpoint: string;
}>;
type Login = Readonly<{ detailsKeys: readonly string[]; remembered: boolean; tokens: Tokens }>;
interface Fixtures {
	clientId?: string;
	userId?: string;
}

const repoRoot = path.resolve(import.meta.dirname, '..');
const cliArgs = process.argv.slice(2);
const failAfterClient = cliArgs.includes('--fail-after-client');

function flagValue(name: string): string | undefined {
	return cliArgs.includes(name) ? cliArgs[cliArgs.indexOf(name) + 1] : undefined;
}

function assertLocal(url: string): void {
	const { hostname } = new URL(url);
	if (!LOCAL_HOSTS.has(hostname)) {
		throw new Error(`refusing ${hostname}: this probe only talks to the local Supabase stack`);
	}
}

function isRecord(value: unknown): value is Json {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function strings(value: unknown): readonly string[] {
	return Array.isArray(value)
		? value.filter((item): item is string => typeof item === 'string')
		: [];
}

function parseJson(text: string): unknown {
	try {
		return text === '' ? {} : JSON.parse(text);
	} catch {
		return {};
	}
}

function isSuccess(status: number): boolean {
	return status >= HTTP_SUCCESS_FIRST && status <= HTTP_SUCCESS_LAST;
}

function readStack(): Stack {
	const override = flagValue('--api-url');
	if (override !== undefined) assertLocal(override);
	const status = parseJson(
		execFileSync('supabase', ['status', '-o', 'json'], { encoding: 'utf8' }),
	);
	const [statusApiUrl, anonKey, serviceRoleKey] = ['API_URL', 'ANON_KEY', 'SERVICE_ROLE_KEY'].map(
		(key) => (isRecord(status) && typeof status[key] === 'string' ? status[key] : undefined),
	);
	const apiUrl = override ?? statusApiUrl;
	if (apiUrl === undefined || anonKey === undefined || serviceRoleKey === undefined) {
		throw new Error(
			'`supabase status -o json` lacks API_URL, ANON_KEY or SERVICE_ROLE_KEY: is the stack up?',
		);
	}
	assertLocal(apiUrl);
	return { anonKey, apiUrl, serviceRoleKey };
}

function configValue(key: keyof typeof CONFIG_PATTERNS): string | null {
	const text = readFileSync(path.join(repoRoot, 'supabase/config.toml'), 'utf8');
	return CONFIG_PATTERNS[key].exec(text)?.groups?.value ?? null;
}

function reuseIntervalSeconds(): number {
	return Number(configValue('reuseInterval') ?? DEFAULT_REUSE_INTERVAL_SECONDS);
}

async function send(request: Request): Promise<Reply> {
	const contentType =
		request.form === undefined ? 'application/json' : 'application/x-www-form-urlencoded';
	const body =
		request.form === undefined
			? JSON.stringify(request.json)
			: new URLSearchParams(request.form).toString();
	const response = await fetch(request.url, {
		body: request.form === undefined && request.json === undefined ? undefined : body,
		headers: { ...request.headers, 'content-type': contentType },
		method: request.method,
		redirect: 'manual',
	});
	const parsed = parseJson(await response.text());
	return {
		body: isRecord(parsed) ? parsed : {},
		location: response.headers.get('location') ?? undefined,
		status: response.status,
	};
}

// The server's own words for a refusal (an error code or a gateway message such as "No API key
// found in request"), never a body that might carry a credential.
function reasonOf(reply: Reply): string | null {
	const { body } = reply;
	const reason = [body.error_code, body.error, body.message, body.msg, body.code].find(
		(value) => typeof value === 'string',
	);
	return typeof reason === 'string' ? reason : null;
}

function fail(step: string, reply: Reply): never {
	const reason = reasonOf(reply);
	throw new Error(`${step} answered ${reply.status}${reason === null ? '' : ` (${reason})`}`);
}

function pkce(): Readonly<{ challenge: string; verifier: string }> {
	const verifier = randomBytes(VERIFIER_BYTES).toString('base64url');
	return { challenge: createHash('sha256').update(verifier).digest('base64url'), verifier };
}

function decodePart(token: string, index: number): Json {
	const part = token.split('.')[index] ?? '';
	const parsed = parseJson(Buffer.from(part, 'base64url').toString('utf8'));
	return isRecord(parsed) ? parsed : {};
}

function describe(reply: Reply, base: string): Redirect {
	if (reply.location === undefined) {
		return { error: null, locationOrigin: null, locationPath: null, status: reply.status };
	}
	const target = new URL(reply.location, base);
	return {
		error: target.searchParams.get('error'),
		locationOrigin: target.origin,
		locationPath: target.pathname,
		status: reply.status,
	};
}

function asUser(probe: Probe, token: string): Headers {
	return { apikey: probe.anonKey, authorization: `Bearer ${token}` };
}

async function outcome(request: Request): Promise<Outcome> {
	const reply = await send(request);
	return { reason: reasonOf(reply), status: reply.status };
}

async function createUser(probe: Probe): Promise<string> {
	const json = { email: probe.email, email_confirm: true, password: probe.password };
	const reply = await send({
		headers: probe.admin,
		json,
		method: 'POST',
		url: `${probe.auth}/admin/users`,
	});
	if (!isSuccess(reply.status)) fail('create user', reply);
	return String(reply.body.id);
}

async function registerClient(probe: Probe): Promise<string> {
	const json = {
		client_name: CLIENT_NAME,
		grant_types: ['authorization_code', 'refresh_token'],
		redirect_uris: [REGISTERED_REDIRECT],
		response_types: ['code'],
		token_endpoint_auth_method: 'none',
	};
	const reply = await send({
		headers: probe.admin,
		json,
		method: 'POST',
		url: `${probe.auth}/admin/oauth/clients`,
	});
	if (!isSuccess(reply.status)) fail('register client', reply);
	return String(reply.body.client_id);
}

async function webSession(probe: Probe): Promise<Tokens> {
	const json = { email: probe.email, password: probe.password };
	const reply = await send({
		headers: probe.anon,
		json,
		method: 'POST',
		url: `${probe.auth}/token?grant_type=password`,
	});
	if (!isSuccess(reply.status)) fail('password sign-in', reply);
	return { access: String(reply.body.access_token), refresh: String(reply.body.refresh_token) };
}

function authorizeUrl(probe: Probe, redirectUri: string, challenge: string): string {
	const query = new URLSearchParams({
		client_id: probe.clientId,
		code_challenge: challenge,
		code_challenge_method: 'S256',
		redirect_uri: redirectUri,
		response_type: 'code',
		state: randomUUID(),
	});
	return `${probe.authorizationEndpoint}?${query.toString()}`;
}

async function authorize(probe: Probe, redirectUri: string, challenge: string): Promise<Reply> {
	return send({
		headers: probe.anon,
		method: 'GET',
		url: authorizeUrl(probe, redirectUri, challenge),
	});
}

async function refreshWith(probe: Probe, refreshToken: string): Promise<Reply> {
	const form = {
		client_id: probe.clientId,
		grant_type: 'refresh_token',
		refresh_token: refreshToken,
	};
	return send({ form, headers: probe.anon, method: 'POST', url: probe.tokenEndpoint });
}

async function refreshStatus(probe: Probe, refreshToken: string): Promise<number> {
	const reply = await refreshWith(probe, refreshToken);
	return reply.status;
}

async function approve(
	probe: Probe,
	consentToken: string,
	authorizationUrl: string,
): Promise<Reply> {
	const reply = await send({
		headers: asUser(probe, consentToken),
		json: { action: 'approve' },
		method: 'POST',
		url: `${authorizationUrl}/consent`,
	});
	if (!isSuccess(reply.status)) fail('consent', reply);
	return reply;
}

// Turns the authorize redirect into an authorization code, approving the consent unless the
// server says it was already given.
async function codeFrom(
	probe: Probe,
	consentToken: string,
	started: Reply,
): Promise<Omit<Login, 'tokens'> & { code: string }> {
	if (started.location === undefined) fail('authorize', started);
	const landing = new URL(started.location, probe.authorizationEndpoint);
	if (landing.pathname !== CONSENT_PATH)
		return { code: landing.searchParams.get('code') ?? '', detailsKeys: [], remembered: true };
	const authorizationUrl = `${probe.auth}/oauth/authorizations/${landing.searchParams.get('authorization_id') ?? ''}`;
	const details = await send({
		headers: asUser(probe, consentToken),
		method: 'GET',
		url: authorizationUrl,
	});
	if (!isSuccess(details.status)) fail('authorization details', details);
	const remembered =
		details.body.authorization_id === undefined && typeof details.body.redirect_url === 'string';
	const approved = remembered ? details : await approve(probe, consentToken, authorizationUrl);
	return {
		code:
			new URL(String(approved.body.redirect_url), probe.authorizationEndpoint).searchParams.get(
				'code',
			) ?? '',
		detailsKeys: Object.keys(details.body).toSorted(),
		remembered,
	};
}

async function exchange(probe: Probe, code: string, verifier: string): Promise<Tokens> {
	const form = {
		client_id: probe.clientId,
		code,
		code_verifier: failAfterClient ? 'wrong-verifier' : verifier,
		grant_type: 'authorization_code',
		redirect_uri: REGISTERED_REDIRECT,
	};
	const reply = await send({ form, headers: probe.anon, method: 'POST', url: probe.tokenEndpoint });
	if (!isSuccess(reply.status)) fail('code exchange', reply);
	return { access: String(reply.body.access_token), refresh: String(reply.body.refresh_token) };
}

async function cliLogin(probe: Probe, consentToken: string): Promise<Login> {
	const { challenge, verifier } = pkce();
	const started = await authorize(probe, REGISTERED_REDIRECT, challenge);
	const { code, detailsKeys, remembered } = await codeFrom(probe, consentToken, started);
	if (code === '') throw new Error('the redirect after consent carried no code');
	return { detailsKeys, remembered, tokens: await exchange(probe, code, verifier) };
}

async function logout(probe: Probe, accessToken: string, scope: 'global' | 'local'): Promise<void> {
	const reply = await send({
		headers: asUser(probe, accessToken),
		method: 'POST',
		url: `${probe.auth}/logout?scope=${scope}`,
	});
	if (!isSuccess(reply.status)) fail(`${scope} logout`, reply);
}

async function redirectFindings(probe: Probe): Promise<Json> {
	const base = probe.authorizationEndpoint;
	return {
		localhostName: describe(await authorize(probe, LOCALHOST_REDIRECT, pkce().challenge), base),
		otherPort: describe(await authorize(probe, OTHER_PORT_REDIRECT, pkce().challenge), base),
		registeredPort: describe(await authorize(probe, REGISTERED_REDIRECT, pkce().challenge), base),
	};
}

// The OAuth calls once more without `apikey`: a browser following the authorize URL sends no
// headers, and a plain OAuth client adds none. A gateway refusal ("No API key found in
// request") means the CLI has to carry the publishable key; GoTrue's own refusal of the
// deliberately invalid code, token or JWT means the call got through.
async function gatewayFindings(probe: Probe): Promise<Json> {
	const none = {};
	const codeForm = {
		client_id: probe.clientId,
		code: 'probe-invalid-code',
		code_verifier: pkce().verifier,
		grant_type: 'authorization_code',
		redirect_uri: REGISTERED_REDIRECT,
	};
	const refreshForm = {
		client_id: probe.clientId,
		grant_type: 'refresh_token',
		refresh_token: 'probe-invalid',
	};
	const logoutHeaders = { authorization: 'Bearer probe-invalid-token' };
	return {
		authorize: await outcome({
			headers: none,
			method: 'GET',
			url: authorizeUrl(probe, REGISTERED_REDIRECT, pkce().challenge),
		}),
		codeExchange: await outcome({
			form: codeForm,
			headers: none,
			method: 'POST',
			url: probe.tokenEndpoint,
		}),
		discovery: await outcome({ headers: none, method: 'GET', url: probe.discoveryUrl }),
		logout: await outcome({
			headers: logoutHeaders,
			method: 'POST',
			url: `${probe.auth}/logout?scope=local`,
		}),
		refresh: await outcome({
			form: refreshForm,
			headers: none,
			method: 'POST',
			url: probe.tokenEndpoint,
		}),
	};
}

async function jwksKids(probe: Probe): Promise<readonly string[]> {
	const reply = await send({
		headers: probe.anon,
		method: 'GET',
		url: `${probe.auth}/.well-known/jwks.json`,
	});
	if (!isSuccess(reply.status)) fail('jwks', reply);
	const keys: unknown = reply.body.keys;
	return Array.isArray(keys)
		? strings(keys.filter((key) => isRecord(key)).map((key: Json) => key.kid))
		: [];
}

// What the API's verifier (apps/api/internal/auth/jwt.go) checks beyond `aud` and `iss`: an ES256
// signature by a key in the JWKS, and a UUID subject.
function tokenFindings(tokens: Tokens, kids: readonly string[]): Json {
	const claims = decodePart(tokens.access, 1);
	const header = decodePart(tokens.access, 0);
	const combinedJsonBytes = Buffer.byteLength(
		JSON.stringify({
			access_token: tokens.access,
			expires_at: claims.exp,
			refresh_token: tokens.refresh,
		}),
	);
	return {
		keychain: {
			fitsWindowsLimit: combinedJsonBytes <= WINDOWS_CREDENTIAL_LIMIT_BYTES,
			limitBytes: WINDOWS_CREDENTIAL_LIMIT_BYTES,
		},
		token: {
			accessBytes: Buffer.byteLength(tokens.access),
			claims: {
				aud: claims.aud,
				client_id: claims.client_id,
				iss: claims.iss,
				lifetimeSeconds: Number(claims.exp) - Number(claims.iat),
				role: claims.role,
				session_id_present: typeof claims.session_id === 'string',
			},
			combinedJsonBytes,
			header: {
				alg: header.alg,
				kidInJwks: typeof header.kid === 'string' && kids.includes(header.kid),
				subIsUuid: typeof claims.sub === 'string' && UUID_PATTERN.test(claims.sub),
			},
			refreshBytes: Buffer.byteLength(tokens.refresh),
		},
	};
}

async function rotate(probe: Probe, refreshToken: string): Promise<string> {
	const reply = await refreshWith(probe, refreshToken);
	if (!isSuccess(reply.status)) fail('refresh', reply);
	const next = reply.body.refresh_token;
	if (typeof next !== 'string' || next === refreshToken)
		throw new Error('refresh did not rotate the refresh token');
	return next;
}

// A fresh CLI session rotated twice: the original token, its child, and the active grandchild.
async function rotateTwice(probe: Probe, consentToken: string): Promise<Chain> {
	const { tokens } = await cliLogin(probe, consentToken);
	const child = await rotate(probe, tokens.refresh);
	const active = await rotate(probe, child);
	return { access: tokens.access, chain: [tokens.refresh, child, active] };
}

async function reuse(probe: Probe, refreshToken: string, active: string): Promise<Reuse> {
	const reply = await refreshWith(probe, refreshToken);
	return { returnsActive: reply.body.refresh_token === active, status: reply.status };
}

// The same two reuses once the interval has passed, then whether the active token survived them.
async function afterIntervalFindings(probe: Probe, consentToken: string): Promise<Json> {
	const { chain } = await rotateTwice(probe, consentToken);
	const waitedSeconds = reuseIntervalSeconds() + REUSE_MARGIN_SECONDS;
	await sleep(waitedSeconds * MILLISECONDS_PER_SECOND);
	const [original, child, active] = chain;
	const reuseParentAfter = await reuse(probe, child, active);
	const reuseGrandparentAfter = await reuse(probe, original, active);
	return {
		activeAfterGrandparentReuse: await refreshStatus(probe, active),
		reuseGrandparentAfter,
		reuseParentAfter,
		waitedSeconds,
	};
}

// Session 1: the claims and sizes, then reuse of an older refresh token inside the interval.
async function refreshFindings(probe: Probe, consentToken: string): Promise<Json> {
	const within = await rotateTwice(probe, consentToken);
	const kids = await jwksKids(probe);
	const [original, child, active] = within.chain;
	const reuseParentWithin = await reuse(probe, child, active);
	const reuseGrandparentWithin = await reuse(probe, original, active);
	const after = await afterIntervalFindings(probe, consentToken);
	return {
		...tokenFindings({ access: within.access, refresh: original }, kids),
		refresh: { ...after, reuseGrandparentWithin, reuseParentWithin, rotates: true },
	};
}

// A second browser signs out locally while a CLI session is open: the reverse of the CLI's
// own local sign-out, which the web's local-by-default sign-out relies on.
async function webLocalKeepsCli(probe: Probe, consentToken: string): Promise<number> {
	const otherBrowser = await webSession(probe);
	const cli = await cliLogin(probe, consentToken);
	await logout(probe, otherBrowser.access, 'local');
	return refreshStatus(probe, cli.tokens.refresh);
}

// Session 2: the consent is remembered, and the CLI's local sign-out leaves the browser alone.
async function localLogoutFindings(
	probe: Probe,
	consentToken: string,
	browser: Tokens,
): Promise<Json> {
	const second = await cliLogin(probe, consentToken);
	await logout(probe, second.tokens.access, 'local');
	const cliLocalEndsCli = await refreshStatus(probe, second.tokens.refresh);
	const browserRefresh = await send({
		headers: probe.anon,
		json: { refresh_token: browser.refresh },
		method: 'POST',
		url: `${probe.auth}/token?grant_type=refresh_token`,
	});
	return {
		browserAccess: isSuccess(browserRefresh.status)
			? String(browserRefresh.body.access_token)
			: browser.access,
		consent: { detailsShape: second.detailsKeys, rememberedOnSecondAuthorize: second.remembered },
		logout: {
			cliLocalEndsCli,
			cliLocalKeepsWeb: browserRefresh.status,
			webLocalKeepsCli: await webLocalKeepsCli(probe, consentToken),
		},
	};
}

// Session 3: a global sign-out from the browser; session 4: revoking the grant.
async function endingFindings(
	probe: Probe,
	consentToken: string,
	browserAccess: string,
): Promise<Json> {
	const third = await cliLogin(probe, consentToken);
	await logout(probe, browserAccess, 'global');
	const webGlobalEndsCli = await refreshStatus(probe, third.tokens.refresh);
	const consentAgain = await webSession(probe);
	const fourth = await cliLogin(probe, consentAgain.access);
	const grantsUrl = `${probe.auth}/user/oauth/grants?client_id=${probe.clientId}`;
	const revoked = await send({
		headers: asUser(probe, consentAgain.access),
		method: 'DELETE',
		url: grantsUrl,
	});
	if (!isSuccess(revoked.status)) fail('revoke grant', revoked);
	return {
		grantRevokeEndsCli: await refreshStatus(probe, fourth.tokens.refresh),
		webGlobalEndsCli,
	};
}

// Each delete is attempted on its own and never throws, so one failure neither skips the other
// nor replaces the error that ended the run; a failure makes the run exit 1.
async function removeOne(probe: Probe, url: string): Promise<void> {
	try {
		const reply = await send({ headers: probe.admin, method: 'DELETE', url });
		if (!isSuccess(reply.status)) throw new Error(`answered ${reply.status}`);
	} catch (error) {
		const why = error instanceof Error ? error.message : String(error);
		process.stderr.write(`oauth-probe | could not delete ${new URL(url).pathname}: ${why}\n`);
		process.exitCode = 1;
	}
}

async function removeFixtures(probe: Probe, fixtures: Readonly<Fixtures>): Promise<void> {
	if (fixtures.clientId !== undefined) {
		await removeOne(probe, `${probe.auth}/admin/oauth/clients/${fixtures.clientId}`);
	}
	if (fixtures.userId !== undefined)
		await removeOne(probe, `${probe.auth}/admin/users/${fixtures.userId}`);
}

async function prepare(): Promise<Probe> {
	const stack = readStack();
	const anon = { apikey: stack.anonKey };
	const discoveryUrl = `${stack.apiUrl}${DISCOVERY_PATH}`;
	const discovery = await send({ headers: anon, method: 'GET', url: discoveryUrl });
	if (!isSuccess(discovery.status)) fail('discovery', discovery);
	const authorizationEndpoint = String(discovery.body.authorization_endpoint);
	const tokenEndpoint = String(discovery.body.token_endpoint);
	assertLocal(authorizationEndpoint);
	assertLocal(tokenEndpoint);
	return {
		admin: { apikey: stack.serviceRoleKey, authorization: `Bearer ${stack.serviceRoleKey}` },
		anon,
		anonKey: stack.anonKey,
		auth: `${stack.apiUrl}/auth/v1`,
		authorizationEndpoint,
		clientId: '',
		codeChallengeMethods: strings(discovery.body.code_challenge_methods_supported),
		discoveryUrl,
		email: `oauth-probe-${randomUUID()}@example.test`,
		password: randomBytes(PASSWORD_BYTES).toString('base64url'),
		tokenEndpoint,
	};
}

async function protocolFindings(probe: Probe): Promise<Json> {
	return {
		discovery: {
			authorizationEndpoint: probe.authorizationEndpoint,
			codeChallengeMethods: probe.codeChallengeMethods,
			path: DISCOVERY_PATH,
			tokenEndpoint: probe.tokenEndpoint,
		},
		gateway: await gatewayFindings(probe),
		redirect: await redirectFindings(probe),
	};
}

async function measure(probe: Probe): Promise<Json> {
	const consent = await webSession(probe);
	const browser = await webSession(probe);
	const protocol = await protocolFindings(probe);
	const first = await refreshFindings(probe, consent.access);
	const local = await localLogoutFindings(probe, consent.access, browser);
	const ending = await endingFindings(probe, consent.access, String(local.browserAccess));
	const logoutFindings = { ...(isRecord(local.logout) ? local.logout : {}), ...ending };
	return {
		...protocol,
		config: {
			refreshTokenReuseIntervalSeconds: reuseIntervalSeconds(),
			siteUrl: configValue('siteUrl'),
		},
		consent: local.consent,
		keychain: first.keychain,
		logout: logoutFindings,
		refresh: first.refresh,
		token: first.token,
	};
}

async function run(): Promise<Json> {
	const base = await prepare();
	const fixtures: Fixtures = {};
	try {
		fixtures.userId = await createUser(base);
		fixtures.clientId = await registerClient(base);
		if (failAfterClient) {
			process.stderr.write(
				'oauth-probe | --fail-after-client: the code exchange will be refused\n',
			);
		}
		return await measure({ ...base, clientId: fixtures.clientId });
	} finally {
		await removeFixtures(base, fixtures);
	}
}

try {
	process.stdout.write(`${JSON.stringify(await run(), null, 2)}\n`);
} catch (error) {
	process.stderr.write(`oauth-probe | ${error instanceof Error ? error.message : String(error)}\n`);
	process.exitCode = 1;
}
