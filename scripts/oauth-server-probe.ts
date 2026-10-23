// Measures how Supabase's OAuth 2.1 server behaves for the CLI, against the local stack only.
//
// The CLI spec (docs/superpowers/specs/2026-10-23-cli-design.md, Part 1) names three risks
// that Supabase's documentation leaves open: whether loopback redirect URIs on other ports or
// hostnames are accepted, which audience the server's access tokens carry, and how refresh
// rotation, the sign-out scopes and a revoked grant reach a CLI session. This script drives a
// complete public-client flow over plain HTTP and prints what it observed as one JSON object:
// it registers a client, authorizes with PKCE, approves the consent as a signed-in user,
// exchanges the code, refreshes, and signs out in each scope. The spec's "Verified" section is
// written from that output, and re-running it is how a later Supabase release gets re-checked.
//
// Run it with the local stack up (`supabase start`): `pnpm probe:oauth`. It reads the stack's
// URL and keys from `supabase status -o json` and refuses any API URL whose host is not
// 127.0.0.1 or localhost, before the first request — `--api-url` exists to prove that refusal.
// No key, token, authorization code or PKCE verifier is ever printed; the report carries
// lengths, claim names and claim values that are not credentials. The user and the client it
// creates are deleted again even when a step fails (`--fail-after-client` forces one, by
// sending a wrong PKCE verifier to the token endpoint).

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
const CONFIG_PATTERNS = {
	reuseInterval: /^refresh_token_reuse_interval\s*=\s*(?<value>\d+)/mu,
	siteUrl: /^site_url\s*=\s*"(?<value>[^"]+)"/mu,
} as const;

type Headers = Readonly<Record<string, string>>;
type Json = Readonly<Record<string, unknown>>;
type Reply = Readonly<{ body: Json; location: string | undefined; status: number }>;
type Tokens = Readonly<{ access: string; refresh: string }>;
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

function assertLocal(apiUrl: string): void {
	const { hostname } = new URL(apiUrl);
	if (!LOCAL_HOSTS.has(hostname)) {
		throw new Error(`refusing ${hostname}: this probe only talks to the local Supabase stack`);
	}
}

function isRecord(value: unknown): value is Json {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
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

// Names what went wrong without echoing a body that might carry a credential.
function fail(step: string, reply: Reply): never {
	const code = [reply.body.error_code, reply.body.error, reply.body.code].find(
		(value) => typeof value === 'string',
	);
	throw new Error(
		`${step} answered ${reply.status}${typeof code === 'string' ? ` (${code})` : ''}`,
	);
}

function pkce(): Readonly<{ challenge: string; verifier: string }> {
	const verifier = randomBytes(VERIFIER_BYTES).toString('base64url');
	return { challenge: createHash('sha256').update(verifier).digest('base64url'), verifier };
}

function claimsOf(accessToken: string): Json {
	const [, payload = ''] = accessToken.split('.');
	const parsed = parseJson(Buffer.from(payload, 'base64url').toString('utf8'));
	return isRecord(parsed) ? parsed : {};
}

function describe(reply: Reply): Redirect {
	if (reply.location === undefined)
		return { error: null, locationOrigin: null, locationPath: null, status: reply.status };
	const target = new URL(reply.location);
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

async function authorize(probe: Probe, redirectUri: string, challenge: string): Promise<Reply> {
	const query = new URLSearchParams({
		client_id: probe.clientId,
		code_challenge: challenge,
		code_challenge_method: 'S256',
		redirect_uri: redirectUri,
		response_type: 'code',
		state: randomUUID(),
	});
	return send({
		headers: probe.anon,
		method: 'GET',
		url: `${probe.authorizationEndpoint}?${query.toString()}`,
	});
}

async function refreshStatus(probe: Probe, refreshToken: string): Promise<number> {
	const form = {
		client_id: probe.clientId,
		grant_type: 'refresh_token',
		refresh_token: refreshToken,
	};
	const reply = await send({ form, headers: probe.anon, method: 'POST', url: probe.tokenEndpoint });
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
	const landing = new URL(started.location);
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
		code: new URL(String(approved.body.redirect_url)).searchParams.get('code') ?? '',
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
	const { code, detailsKeys, remembered } = await codeFrom(
		probe,
		consentToken,
		await authorize(probe, REGISTERED_REDIRECT, challenge),
	);
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
	return {
		localhostName: describe(await authorize(probe, LOCALHOST_REDIRECT, pkce().challenge)),
		otherPort: describe(await authorize(probe, OTHER_PORT_REDIRECT, pkce().challenge)),
		registeredPort: describe(await authorize(probe, REGISTERED_REDIRECT, pkce().challenge)),
	};
}

function tokenFindings(tokens: Tokens): Json {
	const claims = claimsOf(tokens.access);
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
			refreshBytes: Buffer.byteLength(tokens.refresh),
		},
	};
}

// Session 1: the claims, the sizes, and rotation within and after the reuse interval.
async function refreshFindings(probe: Probe, consentToken: string): Promise<Json> {
	const { tokens } = await cliLogin(probe, consentToken);
	const form = {
		client_id: probe.clientId,
		grant_type: 'refresh_token',
		refresh_token: tokens.refresh,
	};
	const rotated = await send({
		form,
		headers: probe.anon,
		method: 'POST',
		url: probe.tokenEndpoint,
	});
	if (!isSuccess(rotated.status)) fail('refresh', rotated);
	const reuseWithinInterval = await refreshStatus(probe, tokens.refresh);
	const waitedSeconds =
		Number(configValue('reuseInterval') ?? DEFAULT_REUSE_INTERVAL_SECONDS) + REUSE_MARGIN_SECONDS;
	await sleep(waitedSeconds * MILLISECONDS_PER_SECOND);
	const reuseAfterInterval = { status: await refreshStatus(probe, tokens.refresh), waitedSeconds };
	const refresh = {
		reuseAfterInterval,
		reuseWithinInterval,
		rotates: String(rotated.body.refresh_token) !== tokens.refresh,
	};
	return { ...tokenFindings(tokens), refresh };
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
	const json = { refresh_token: browser.refresh };
	const browserRefresh = await send({
		headers: probe.anon,
		json,
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

async function removeFixtures(probe: Probe, fixtures: Readonly<Fixtures>): Promise<void> {
	const targets = [
		fixtures.clientId === undefined
			? undefined
			: `${probe.auth}/admin/oauth/clients/${fixtures.clientId}`,
		fixtures.userId === undefined ? undefined : `${probe.auth}/admin/users/${fixtures.userId}`,
	].filter((url) => url !== undefined);
	for (const url of targets) {
		// eslint-disable-next-line no-await-in-loop -- two deletes, and their order keeps the client from outliving its user
		const reply = await send({ headers: probe.admin, method: 'DELETE', url });
		if (!isSuccess(reply.status))
			process.stderr.write(
				`oauth-probe | could not delete ${new URL(url).pathname} (${reply.status})\n`,
			);
	}
}

async function prepare(): Promise<Probe> {
	const stack = readStack();
	const anon = { apikey: stack.anonKey };
	const discovery = await send({
		headers: anon,
		method: 'GET',
		url: `${stack.apiUrl}${DISCOVERY_PATH}`,
	});
	if (!isSuccess(discovery.status)) fail('discovery', discovery);
	return {
		admin: { apikey: stack.serviceRoleKey, authorization: `Bearer ${stack.serviceRoleKey}` },
		anon,
		anonKey: stack.anonKey,
		auth: `${stack.apiUrl}/auth/v1`,
		authorizationEndpoint: String(discovery.body.authorization_endpoint),
		clientId: '',
		email: `oauth-probe-${randomUUID()}@example.test`,
		password: randomBytes(PASSWORD_BYTES).toString('base64url'),
		tokenEndpoint: String(discovery.body.token_endpoint),
	};
}

async function measure(probe: Probe): Promise<Json> {
	const consent = await webSession(probe);
	const browser = await webSession(probe);
	const redirect = await redirectFindings(probe);
	const first = await refreshFindings(probe, consent.access);
	const local = await localLogoutFindings(probe, consent.access, browser);
	const ending = await endingFindings(probe, consent.access, String(local.browserAccess));
	const logoutFindings = { ...(isRecord(local.logout) ? local.logout : {}), ...ending };
	const config = {
		refreshTokenReuseIntervalSeconds: Number(configValue('reuseInterval')),
		siteUrl: configValue('siteUrl'),
	};
	const discovery = {
		authorizationEndpoint: probe.authorizationEndpoint,
		path: DISCOVERY_PATH,
		tokenEndpoint: probe.tokenEndpoint,
	};
	return {
		config,
		consent: local.consent,
		discovery,
		keychain: first.keychain,
		logout: logoutFindings,
		redirect,
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
		if (failAfterClient)
			process.stderr.write(
				'oauth-probe | --fail-after-client: the code exchange will be refused\n',
			);
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
