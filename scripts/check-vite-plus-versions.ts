// Fails when the toolchain pnpm installed is not the one `vite-plus` was released with.
//
// `vite-plus` pins `vite` (as an alias of its own core package), `vitest` and its
// `@vitest/browser-playwright` peer to exact versions, and the `vite@*` and `vitest@*`
// overrides in `pnpm-workspace.yaml` force the catalog's versions onto those pins. A
// catalog entry that drifts from them therefore installs cleanly into a combination
// `vite-plus` was never released with, and nothing else reports it.
//
// It reads what is installed, not what `pnpm-workspace.yaml` says, so it needs no YAML
// parser and also notices an override that stopped doing its job. Run it after
// `pnpm install`, as `pnpm check:vite-plus`; CI does, in the "format, lint & typecheck" job.

import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';

type Manifest = Readonly<{ name: string; version: string }>;

/** `drift` is true for a version that differs, false for a package that could not be read at all. */
type Problem = Readonly<{ drift: boolean; line: string }>;

type Expectation = Readonly<{
	fromDir: string;
	/** How `fromDir` is named in the report. */
	fromLabel: string;
	specifier: string;
	/** The package name it must carry, when `vite-plus` declares one (only `vite`'s alias does). */
	name?: string;
	version: string;
}>;

const ROOT = path.join(import.meta.dirname, '..');

/**
 * @param value - Anything parsed from JSON.
 * @returns Whether it is a plain object rather than an array, `null` or a primitive.
 */
function isRecord(value: unknown): value is Readonly<Record<string, unknown>> {
	return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * @param error - Whatever a `catch` clause received.
 * @returns A one-line reason, led by Node's error code when it has one.
 */
function reasonOf(error: unknown): string {
	if (isRecord(error) && typeof error.code === 'string') return error.code;
	return error instanceof Error ? error.message : String(error);
}

/**
 * @param file - A `package.json` to read.
 * @returns Its parsed contents.
 * @throws {Error} When the file is not a JSON object.
 */
function readJson(file: string): Readonly<Record<string, unknown>> {
	const parsed: unknown = JSON.parse(readFileSync(file, 'utf8'));
	if (!isRecord(parsed)) throw new Error(`${file} is not a JSON object`);
	return parsed;
}

/**
 * @param vitePlus - The parsed `package.json` of `vite-plus`.
 * @param section - `dependencies` or `peerDependencies`.
 * @param name - The package within that section.
 * @returns The version string `vite-plus` declares for it.
 * @throws {Error} When `vite-plus` declares nothing for it: an unreadable declaration is never a pass.
 */
function declaredVersion(
	vitePlus: Readonly<Record<string, unknown>>,
	section: string,
	name: string,
): string {
	const entries = vitePlus[section];
	const value = isRecord(entries) ? entries[name] : undefined;
	if (typeof value !== 'string' || value === '') {
		throw new Error(
			`vite-plus's package.json declares no ${section}["${name}"], so there is nothing to check the install against`,
		);
	}
	return value;
}

/**
 * Reads `dependencies.vite`, which `vite-plus` writes as an alias,
 * `npm:@voidzero-dev/vite-plus-core@1.3.0`, so the check has to know both halves.
 *
 * @param declared - The alias as written in `vite-plus`'s `package.json`.
 * @returns The real package's name and version.
 * @throws {Error} When it is not an `npm:<name>@<version>` alias.
 */
function parseAlias(declared: string): Manifest {
	const match = /^npm:(?<name>@?[^@]+)@(?<version>.+)$/u.exec(declared);
	const { name, version } = match?.groups ?? {};
	if (name === undefined || version === undefined) {
		throw new Error(
			`vite-plus declares dependencies.vite as "${declared}", which is not an npm:<name>@<version> alias`,
		);
	}
	return { name, version };
}

/**
 * @param fromDir - The directory to resolve from, the way `require` would from a file in it.
 * @param specifier - A package name.
 * @returns The `name` and `version` of the `package.json` it resolves to.
 * @throws {Error} When the package does not resolve, or its manifest has no name and version.
 */
function readInstalled(fromDir: string, specifier: string): Manifest {
	const manifestPath = createRequire(path.join(fromDir, 'package.json')).resolve(
		`${specifier}/package.json`,
	);
	const { name, version } = readJson(manifestPath);
	if (typeof name !== 'string' || typeof version !== 'string') {
		throw new TypeError(`${manifestPath} has no name and version`);
	}
	return { name, version };
}

/**
 * @param found - The installed package.
 * @param expected - What `vite-plus` declares for it.
 * @returns A line describing the difference, or `undefined` when there is none.
 */
function compare(found: Manifest, expected: Expectation): string | undefined {
	const { fromLabel, name, specifier, version } = expected;
	if (name !== undefined && found.name !== name) {
		return `${specifier} resolved from ${fromLabel} is the package ${found.name}, but vite-plus declares ${name}`;
	}
	if (found.version !== version) {
		return `${specifier} ${found.version} resolved from ${fromLabel}, but vite-plus declares ${version}`;
	}
	return undefined;
}

/**
 * @param expected - Which installed package to look at, from where, and what it must be.
 * @returns What is wrong with it, or `undefined` when it is what `vite-plus` declares.
 */
function problemWith(expected: Expectation): Problem | undefined {
	try {
		const line = compare(readInstalled(expected.fromDir, expected.specifier), expected);
		return line === undefined ? undefined : { drift: true, line };
	} catch (error) {
		return {
			drift: false,
			line: `${expected.specifier} cannot be resolved from ${expected.fromLabel} (${reasonOf(error)})`,
		};
	}
}

/**
 * @returns The directory `vite-plus` was installed into, and its parsed `package.json`.
 * @throws {Error} When `vite-plus` cannot be resolved from the repository root or read.
 */
function readVitePlus(): Readonly<{ dir: string; manifest: Readonly<Record<string, unknown>> }> {
	try {
		const manifestPath = createRequire(path.join(ROOT, 'package.json')).resolve(
			'vite-plus/package.json',
		);
		return { dir: path.dirname(manifestPath), manifest: readJson(manifestPath) };
	} catch (error) {
		throw new Error(
			`vite-plus cannot be resolved from the repository root (${reasonOf(error)}); has pnpm install run?`,
			{ cause: error },
		);
	}
}

/**
 * @param vitePlus - The parsed `package.json` of `vite-plus`.
 * @returns The three versions it declares.
 * @throws {Error} When one of them is missing or unreadable.
 */
function readDeclared(
	vitePlus: Readonly<Record<string, unknown>>,
): Readonly<{ core: Manifest; playwright: string; vitest: string }> {
	return {
		core: parseAlias(declaredVersion(vitePlus, 'dependencies', 'vite')),
		playwright: declaredVersion(vitePlus, 'peerDependencies', '@vitest/browser-playwright'),
		vitest: declaredVersion(vitePlus, 'dependencies', 'vitest'),
	};
}

/**
 * @param declared - What `vite-plus` declares.
 * @param vitePlusDir - Where `vite-plus` was installed, so its own copies can be resolved.
 * @returns Every installed package that has to equal one of those versions, with where to look for it.
 */
function expectationsFor(
	declared: ReturnType<typeof readDeclared>,
	vitePlusDir: string,
): readonly Expectation[] {
	const webDir = path.join(ROOT, 'apps', 'web');
	return [
		{
			fromDir: vitePlusDir,
			fromLabel: 'vite-plus',
			name: declared.core.name,
			specifier: 'vite',
			version: declared.core.version,
		},
		{
			fromDir: vitePlusDir,
			fromLabel: 'vite-plus',
			specifier: 'vitest',
			version: declared.vitest,
		},
		{ fromDir: webDir, fromLabel: 'apps/web', specifier: 'vitest', version: declared.vitest },
		{
			fromDir: webDir,
			fromLabel: 'apps/web',
			specifier: '@vitest/browser-playwright',
			version: declared.playwright,
		},
	];
}

/**
 * @throws {Error} When `vite-plus` cannot be read, or declares something unreadable.
 */
function main(): void {
	const vitePlus = readVitePlus();
	const declared = readDeclared(vitePlus.manifest);
	const problems = expectationsFor(declared, vitePlus.dir)
		.map((expected) => problemWith(expected))
		.filter((problem) => problem !== undefined);
	if (problems.length === 0) {
		console.log(
			`check:vite-plus | the installed Vite+ toolchain matches vite-plus: ${declared.core.name} ${declared.core.version}, vitest ${declared.vitest}, @vitest/browser-playwright ${declared.playwright}`,
		);
		return;
	}
	for (const { line } of problems) console.error(`check:vite-plus | ${line}`);
	if (problems.some(({ drift }) => drift)) {
		console.error(
			'check:vite-plus | the catalog in pnpm-workspace.yaml must match what vite-plus declares: set its vite, vitest and @vitest/browser-playwright entries to those versions, then run pnpm install',
		);
	}
	process.exitCode = 1;
}

try {
	main();
} catch (error) {
	console.error(`check:vite-plus | ${error instanceof Error ? error.message : String(error)}`);
	process.exitCode = 1;
}
