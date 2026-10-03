# Server-side source maps for `apps/web`: spike and decision

## Decision (2026-10-03): not implemented, deliberately

Only the **client** source maps are uploaded to Sentry (and then deleted). The server bundle's maps are not uploaded, so a server-side error in Sentry shows frames in Nitro's bundled files (`chunks/_/routes-….mjs:123`) rather than in `src/…ts`.

That was a choice, made after the spike below showed it can be done:

- **The gain is small.** Nitro's server output is not minified. Server frames already show readable JavaScript with the real function and variable names; resolving them would only add the original TypeScript file, line and context.
- **The cost is ongoing.** The working configuration, about 50 lines plus a pinned `@sentry/bundler-plugins` devDependency, depends on four undocumented or experimental internals: the plugin name `sentry-vite-plugin`, Vite's `applyToEnvironment`, Nitro's `experimental.sourcemapMinify`, and `nitroV2Plugin`'s map pass-through and its overwrite of `rollupConfig.plugins`. Any of them breaking degrades silently to unresolved frames.
- **Server errors are rare** at the instance's current size.

**Revisit** when debugging a server error without source frames becomes a real cost, or when the app moves to Nitro v3 (or `nitroV2Plugin` is reworked). Either way, much of the plumbing below may change. The proposal below is the starting point, but every internal it names has to be re-checked against the versions in use then, and it should ship with the build assertion described under Q5.

**Known as of today, and harmless:** on Vercel the server bundle carries 57 debug-ID snippets injected by the Vite-level Sentry plugin into the SSR environment. Nothing is ever uploaded for them. The server maps stay in the function directory, which Vercel does not serve over HTTP.

---

## The spike

Date: 2026-10-03. Scope: `apps/web` (TanStack Start + `nitroV2Plugin` + `@sentry/tanstackstart-react` 11.1.0, Vite 8.3.0 / rolldown 1.2.8, nitropack 2.13.4, rollup 4.63.1).

**Answer: yes.** You need about 50 lines in `vite.config.ts` and one new direct devDependency (`@sentry/bundler-plugins`, pinned to the SDK's version, already in the lockfile as a transitive dependency). No build-command change and no CLI are needed. Everything up to the upload was verified offline. That covers 43/43 final server files carrying exactly one debug ID, chained maps that reach `src/*.ts(x)` with `sourcesContent`, and a real exception thrown from a built server chunk resolving back to `src/lib/audit-filters.ts:59` through the same debug-ID lookup the SDK and Sentry use. The upload itself, and Sentry's handling of a real event, can only be confirmed on a Vercel preview.

**Correction to the starting assumption.** The maps first inspected (`sources: ../../../../assets/routes-*.js`) come from a local build without `SENTRY_AUTH_TOKEN`. In that build the Sentry plugin is not registered, so the SSR build emits no maps. On Vercel the plugin _is_ registered, and today's production config (reproduced here minus the token, "exp1") already chains server maps to `src/`. Those maps are still unusable, for four reasons:

1. They have no `sourcesContent`.
2. Six of them, including the main app chunk `router-*.mjs` with 57 `src/` files, have `mappings: ""`.
3. Every `src/` path is off by two directories.
4. The server output carries 57 debug-ID snippets (41 of 43 files have one, 3 files have several, `router-*.mjs` has 15). None of their IDs is ever uploaded.

The experiment logs and scripts were throwaway and are not kept.

Paths below abbreviate `node_modules/.pnpm/<pkg>@<ver>_<peers>/node_modules/<pkg>` as `<pkg>`.

---

### Q1: Can the SSR build emit source maps that reach `src/*.ts(x)` with `sourcesContent`?

**Yes, and with the Sentry plugin registered it already does.**

- `sentryTanstackStart` adds `sentry-tanstackstart-react-source-maps`, which sets the top-level `build.sourcemap: 'hidden'` (`@sentry/tanstackstart-react/build/esm/vite/sourceMaps.js:77-122`). Vite 8 environments inherit top-level `build` options, so the `ssr` environment emits maps as well. The build log prints `[Sentry] Enabled source map generation … 'hidden'` once per environment.
- `nitroV2Plugin` builds the SSR environment with `write: false` (`@tanstack/nitro-v2-vite-plugin/dist/esm/index.js:29-36`). The SSR maps therefore never reach disk; they exist only as `.map` assets in the in-memory bundle that the plugin hands to Nitro (`index.js:10-22`).
- Rolldown includes `sourcesContent` by default. When the final maps lacked it, Nitro was the cause (see Q2), not Vite.
- One fix is needed on the Vite side: a path skew. `nitroV2Plugin` registers each SSR chunk under `resolve(fileName)`, which is `<cwd>/assets/x.js` (`index.js:95`), rather than under its real location `<cwd>/dist/server/assets/x.js`. The chunk's map sources (`../../../src/…`) are relative to the real location, so after chaining they resolve to `<repo>/src/…` instead of `<repo>/apps/web/src/…`. Exp1 shows this in the final maps as `../../../../../../src/routes/__root.tsx` from `.output/server/chunks/_/`. The fix is to make SSR map sources absolute with `environments.ssr.build.rolldownOptions.output.sourcemapPathTransform: (rel, mapPath) => path.resolve(path.dirname(mapPath), rel)`. Vite uses this exact pattern itself (`vite/dist/node/chunks/node.js:37451`), and rolldown types it (`rolldown/dist/shared/define-config-DjHYbH6S.d.mts:523`). Rollup relativizes the paths again in Nitro's output, so the final maps contain **0 absolute `/Users/…` paths** (checked).

### Q2: Does Nitro chain those maps into `.output/server/**/*.mjs.map`?

**Yes. The chaining happens through `nitroV2Plugin` internals, but three Nitro defaults degrade the result, and all three can be overridden.**

- The chaining happens because `nitroV2Plugin`'s `virtual-bundle` plugin returns `{ code, map }` from `load`, with `map` set to the SSR chunk's `.map` asset (`index.js:82-115`). Rollup collapses that input map into Nitro's output map.
- Three defaults in nitropack's `dist/rollup/index.mjs` work against it:
  - `output.sourcemapExcludeSources: true` (`:1811`) means no `sourcesContent`.
  - `sourcemapMinify` (`:1561-1577`, enabled at `:2118-2119`) sets `mappings = ""` on **any** map that has at least one `node_modules` source. That blanks `router-*.mjs.map`, whose 58 sources are 57 `src/` files plus 1 `node_modules` file. The option is typed as `experimental.sourcemapMinify?: false` (`nitropack/dist/shared/nitro.D682J6aL.d.ts:1029-1031`).
  - Nitro does chain into its own esbuild transform; that is harmless.
- Nitro config passes through (`...nitroConfig`, `index.js:55`), **but `nitroV2Plugin` overwrites `rollupConfig.plugins`** with its own virtual-bundle plugin (`index.js:61-64`). A plugin passed via `nitroConfig.rollupConfig.plugins` would therefore be dropped silently. The supported way in is Nitro's typed `rollup:before` hook (`NitroHooks`, `nitro.D682J6aL.d.ts:787`). It is registered from `nitroConfig.hooks` (`nitropack/dist/core/index.mjs:1133`) and called with the final rollup config right before the build (`core/index.mjs:1697-1700`). `rollupConfig` is `defu(nitro.options.rollupConfig, defaults)` (`rollup/index.mjs:1789`), so overriding `sourcemapExcludeSources` from config would also work. Setting it in the same hook keeps everything in one place.

Result across all 43 final maps:

|  | exp1 (today's Vercel config, no token) | proposal |
| --- | --- | --- |
| maps whose `sources` reach `src/` | 36 | 36 (the other 7 are pure node_modules/virtual) |
| path correctness | off by 2 dirs (`../../../../../../src/…`) | correct (`../../../../src/…` → `apps/web/src/…`) |
| maps with `sourcesContent` | 0 | 42 (`index.mjs.map` has no sources) |
| maps with empty `mappings` | 6 (incl. `router`, `entry`) | 0 |
| `sourcesContent` identical to the file on disk | n/a | 136 of 137 `src/` sources (the odd one is the virtual `app.css?url`) |

Resolution checks with `@jridgewell/trace-mapping` on the proposal build:

- `audit-filters-*.mjs:14:83` resolves to `src/lib/audit-filters.ts:59:70`, and the line matches the file.
- `router-*.mjs:1253:2` (previously blanked) resolves to `src/i18n/index.ts:19:1`, and the line matches.
- `teams._teamSlug.audit-log-*.mjs:66:16` resolves to `src/routes/_authed/teams.$teamSlug.audit-log.tsx?tsr-split=component:220:15` (name `useTranslation`), and line 220 of the file matches.

### Q3: Can debug IDs be injected into the final Nitro output?

**Yes. Use `sentryRollupPlugin` from `@sentry/bundler-plugins/rollup`, pushed into Nitro's rollup config in `rollup:before`. Injection works without an auth token. The existing Vite-level plugin must be kept out of the SSR environment, though, or the result is silently wrong.**

- `@sentry/bundler-plugins` 11.1.0 exports `./rollup` publicly (its `package.json` `exports`). The plugin's `renderChunk` prepends the debug-ID snippet and returns a MagicString map, which Rollup chains (`@sentry/bundler-plugins/build/esm/rollup/index.js:117-144`). Only release creation and upload need the token (`build-plugin-manager.js:226-250, 511-539`). Without a token, the build logs "No auth token provided. Will not upload source maps" once per stage and carries on.
- **Why the SSR restriction is mandatory.** The injector skips any chunk whose first 6,000 characters already contain `_sentryDebugIdIdentifier` (`rollup/index.js:14-21, 125`). Today the Vite-level `sentry-vite-plugin` injects into every SSR chunk, and Nitro merges several SSR chunks into one file. On such a file:
  - the SDK maps the file to the **last** snippet executed (`@sentry/core/build/esm/utils/debug-ids.js:35-44`),
  - the upload labels the file with the **first** ID it finds (`debug-id-upload.js:56-65`),
  - so the two disagree.

  I tested this directly by building the proposal without the restriction ("exp2-norestrict"). Three files ended up with mismatched IDs, among them `router-*.mjs` with 15 IDs, and nothing failed.

- **The restriction.** Set `applyToEnvironment: (env) => env.name === 'client'` on the plugin named `sentry-vite-plugin` (name formed at `rollup/index.js:190`; filtering happens in Vite at `vite/dist/node/chunks/node.js:3350-3369`, and it works with `nitroV2Plugin`'s `sharedPlugins: true`). With the restriction and no Nitro-stage plugin ("exp2-norollup"), the server output has **0** snippets. The client still has 54/54 snippets, and the names of all 54 client JS assets (content hashes) are **identical** to today's config.
- **Proposal result:** 43/43 final `.mjs` files carry exactly one debug ID, and 0 carry more than one. With `sourcemaps.disable: 'disable-upload'` (the mode that also stamps files on disk, `rollup/index.js:145-161, 200`), 43/43 files have snippet ID, `//# debugId=` comment and the map's `debugId` all equal. In normal upload mode the comment and the map's `debugId` are written only into the temporary copies that get uploaded (`debug-id-upload.js:14-55`). That is by design and enough, because the runtime uses the snippet.
- **Offline end-to-end** (a throwaway script run on the exact proposal build):
  1. It prepares the upload artifacts with the plugin's own `prepareBundleForDebugIdUpload`: 43 bundles plus 43 maps, without any network.
  2. It imports the real `audit-filters-*.mjs` and calls `parseAuditFilters(undefined)`.
  3. It parses the stack with `@sentry/core`'s Node parser and maps file → debug ID with `getFilenameToDebugIdMap`. The ID is `11bba4e9-…`, which matches the file.
  4. It picks the artifact with that ID and resolves the frame. Result: **`src/lib/audit-filters.ts:59:16`**, with the context line `typeof search.page === 'number' && …` taken from `sourcesContent`.
- **Rejected: post-build CLI.**
  - `sentry` 0.44.1 has `sourcemap inject`, but it refuses to run without auth even locally (`Error: Not authenticated. Run 'sentry auth login' first.`; telemetry and update checks were disabled and the URL pointed at `127.0.0.1:9` for the test).
  - It would need to become a direct dependency plus a change to the build command.
  - It would still need the same SSR restriction.
  - The `@sentry/cli@2.58.6` folder in `node_modules/.pnpm` is a stale store entry. It is not in `pnpm-lock.yaml`.

### Q4: Do the client upload and the client-map deletion behave the same? How are server maps uploaded and deleted?

**The client is unchanged. The server gets its own upload and deletion.**

- **Client.** `nitroV2Plugin` builds the client environment completely first (`writeBundle` → upload → delete, awaited). Then it builds SSR, and only then runs Nitro, which copies `dist/client` into the public output (`index.js:44-46, 72-74`). The SSR environment never uploaded, because `write: false` means it has no `writeBundle` (consistent with the Vercel logs). Restricting `sentry-vite-plugin` to the client therefore changes nothing for the client. Same asset hashes, same snippets, and the client upload path is unchanged.
- **Deletion order** (proposal with deletion enabled, Vercel preset): `dist/client` has 0 maps, `.vercel/output/static` has 0 maps, and `.vercel/output/functions/__fallback.func` has 0 maps. The client plugin's `./.output/**/*.map` and `./.vercel/output/**/*.map` globs run before Nitro has written anything, so they only ever match leftovers of a previous local build; the log shows them deleting stale maps. **On Vercel today the server maps in `__fallback.func` are never deleted.**
- **Server upload.** The Nitro-stage plugin uploads in its own `writeBundle`, with `outputOptions.dir` set to the function directory (`rollup/index.js:162-189`). `sourcemaps.ignore: ['**/node_modules/**']` stops it from reading the ~3,100 traced `node_modules` JS files. Its own `filesToDeleteAfterUpload` then deletes the server maps once the upload has finished (`build-plugin-manager.js:484-507`).
- **Does deleting the server maps matter?** Not for exposure: a Vercel function's files are not served over HTTP; only `.vercel/output/static` is. I still recommend deleting them, for three reasons:
  - it matches the existing client policy and its comment,
  - it keeps the function bundle ~3.4 MB smaller,
  - it keeps the original server TypeScript (comments included) out of the deployment.
- **Release management stays with the client plugin.** The server plugin gets `release: { inject: false, create: false, finalize: false, setCommits: false, deploy: false }`. Otherwise every Vercel build would also create and finalize the release a second time, and write a second deploy record (on Vercel the deploy record is enabled automatically, `options-mapping.js:61-67`). The release name is still auto-detected from `VERCEL_GIT_COMMIT_SHA` (`utils.js:165`) for the upload.

### Q5: Cost and risk

**Repo changes:**

- `apps/web/vite.config.ts`: about 50 lines (below).
- `apps/web/package.json`: new devDependency `@sentry/bundler-plugins` at exactly `11.1.0`, plus the lockfile update. The package is already resolved in the lockfile (`pnpm-lock.yaml:2247, 8126`), so no new code enters the tree.
- Renovate should move it together with `@sentry/tanstackstart-react`. If the two drift apart, two copies get installed. That is low risk, because the snippet and the `_sentryDebugIds` global have been stable for years.
- The CLAUDE.md entry "Web source maps are uploaded and then deleted" should gain the server half: why `applyToEnvironment`, why `rollup:before`, why `sourcemapMinify: false`.

**Build time:** no measurable local change (no upload). The proposal took 11.6–11.7 s and today's config 11.1–15.9 s, over 4 runs each; cold first runs reached 31.6 s and 38.9 s. On Vercel, one more upload of roughly 0.8 MB of code plus 3.4 MB of maps per deployment, previews included, adds a few seconds. I did not measure this.

**Bundle size** (server app code, excluding traced `node_modules`):

| config | `.mjs` bytes | debug-ID snippets | `.map` bytes on disk |
| --- | --- | --- | --- |
| no Sentry (local build, no token) | 809,800 | 0 | – |
| today on Vercel (exp1) | 852,778 | 57, never uploaded | 257,668 |
| proposal | 826,705 (−26 KB vs today) | 43 × ~389 B = 16.7 KB | 3,404,187 before deletion, 0 after |

The client bundle does not change.

**Risks:**

1. **Silent failure is the main risk.** Every way this breaks degrades to "frames not resolved"; none of them fails the build. The four internals it relies on:
   - the Sentry plugin name `sentry-vite-plugin`, which the proposal guards with a `throw`;
   - Vite's Environment API `applyToEnvironment`;
   - Nitro's `experimental.sourcemapMinify`;
   - `nitroV2Plugin`'s map pass-through, its path registration and its overwrite of `rollupConfig.plugins`.

   Optional hardening, about 15 lines: a `generateBundle` check after the Sentry plugin that fails the Vercel build unless every chunk has exactly one `sentry-dbid-` and every `src/` map has `sourcesContent`.

2. **Nitro v3 or a `nitroV2Plugin` rework would invalidate parts of this.** If TanStack fixes the path skew, `sourcemapPathTransform` becomes redundant but stays harmless.
3. **CI never exercises this path.** It is gated on `SENTRY_AUTH_TOKEN`, as the client upload already is. Only Vercel builds exercise it.

**Only verifiable on Vercel:**

- The upload succeeds: two "Successfully uploaded source maps" lines in the build log (`[sentry-vite-plugin]` and `[sentry-rollup-plugin]`), and no `.map` left in `__fallback.func`.
- Runtime filenames under `/var/task/…` map to debug IDs, and a deliberately thrown server-side error shows up in Sentry with `src/…` frames and context lines.
- The build-time delta of the extra upload.

### Proposal, not applied

Type-checked with `tsc` 7.0.2: exit 0, as was the tracked config used as a control. The exact code, with the token gate forced on, `telemetry: false` and no token, was built with both the node-server and the `vercel` Nitro presets. The import resolves only after adding `@sentry/bundler-plugins` as a devDependency.

```ts
import path from 'node:path';

import { sentryRollupPlugin } from '@sentry/bundler-plugins/rollup';
import { sentryTanstackStart } from '@sentry/tanstackstart-react/vite';
import tailwindcss from '@tailwindcss/vite';
import { devtools } from '@tanstack/devtools-vite';
import { nitroV2Plugin } from '@tanstack/nitro-v2-vite-plugin';
import { tanstackStart } from '@tanstack/react-start/plugin/vite';
import viteReact from '@vitejs/plugin-react';
import { defineConfig, type PluginOption, type UserConfig } from 'vite';

const sentryEnabled =
	process.env.SENTRY_AUTH_TOKEN !== undefined && process.env.SENTRY_AUTH_TOKEN !== '';

const sentryCredentials = {
	authToken: process.env.SENTRY_AUTH_TOKEN,
	org: process.env.SENTRY_ORG,
	project: process.env.SENTRY_PROJECT,
};

/**
 * Keeps `sentry-vite-plugin` (debug-ID snippets + upload) out of the SSR environment.
 * Nitro re-bundles the SSR chunks, so snippets injected there end up several to a
 * file, under debug IDs nothing is ever uploaded for; the Nitro-stage plugin below
 * injects exactly one per final file instead, and skips any file that already has one.
 * @param plugins - What `sentryTanstackStart()` returned.
 * @returns The same plugins, the upload plugin restricted to the client environment.
 */
function uploadPluginOnClientOnly(plugins: PluginOption[]): PluginOption[] {
	let found = false;
	const restricted = plugins.map((plugin) => {
		if (
			!plugin ||
			typeof plugin !== 'object' ||
			!('name' in plugin) ||
			plugin.name !== 'sentry-vite-plugin'
		) {
			return plugin;
		}
		found = true;
		return {
			...plugin,
			applyToEnvironment: (environment: { name: string }) => environment.name === 'client',
		};
	});
	if (!found) {
		throw new Error(
			'sentryTanstackStart() no longer returns "sentry-vite-plugin"; server debug IDs would break silently.',
		);
	}
	return restricted;
}

const sentryPlugins = sentryEnabled
	? uploadPluginOnClientOnly(
			sentryTanstackStart({
				...sentryCredentials,
				sourcemaps: {
					filesToDeleteAfterUpload: [
						'./dist/**/*.map',
						'./.output/**/*.map',
						'./.vercel/output/**/*.map',
					],
				},
			}),
		)
	: [];

/** Server half: chained maps with sources, one debug ID per final Nitro file, uploaded then deleted. */
const nitroServerSourceMaps: NonNullable<Parameters<typeof nitroV2Plugin>[0]> = sentryEnabled
	? {
			experimental: { sourcemapMinify: false },
			hooks: {
				'rollup:before'(_nitro, rollupConfig) {
					rollupConfig.output.sourcemapExcludeSources = false;
					rollupConfig.plugins = [
						rollupConfig.plugins,
						sentryRollupPlugin({
							...sentryCredentials,
							release: {
								inject: false,
								create: false,
								finalize: false,
								setCommits: false,
								deploy: false,
							},
							sourcemaps: {
								ignore: ['**/node_modules/**'],
								rewriteSources: (source, _map, context) =>
									path
										.relative(process.cwd(), path.resolve(context?.mapDir ?? process.cwd(), source))
										.replace(/\?.*$/u, ''),
								filesToDeleteAfterUpload: [
									'./.output/server/**/*.map',
									'./.vercel/output/functions/**/*.map',
								],
							},
						}),
					];
				},
			},
		}
	: {};

const ssrSourcePaths: UserConfig['environments'] = sentryEnabled
	? {
			ssr: {
				build: {
					rolldownOptions: {
						output: {
							sourcemapPathTransform: (relativeSourcePath, sourcemapPath) =>
								path.resolve(path.dirname(sourcemapPath), relativeSourcePath),
						},
					},
				},
			},
		}
	: {};

const config = defineConfig({
	define: {
		'import.meta.env.VITE_SENTRY_ENVIRONMENT': JSON.stringify(
			process.env.VERCEL_ENV ?? 'development',
		),
		'import.meta.env.VITE_SENTRY_RELEASE': JSON.stringify(process.env.VERCEL_GIT_COMMIT_SHA ?? ''),
	},
	environments: ssrSourcePaths,
	plugins: [
		devtools(),
		tailwindcss(),
		tanstackStart(),
		nitroV2Plugin({ compatibilityDate: '2026-09-04', ...nitroServerSourceMaps }),
		viteReact(),
		...sentryPlugins,
	],
	resolve: { tsconfigPaths: true },
});

export default config;
```

Notes on the proposal:

- The existing comments in `vite.config.ts` are omitted here for brevity and should be kept.
- `rewriteSources` only changes the display name: the default hook turns `../../../../src/x.ts` into the same unwieldy relative string. The custom one gives `src/lib/audit-filters.ts` and strips the `?tsr-split=…` / `?tss-serverfn-split` suffixes; their `sourcesContent` equals the original file. The client upload has the same cosmetic issue; giving it the same hook is optional.
- `rollupConfig.plugins = [existing, sentry]` rather than `.push(...)`, because Nitro types `plugins` as rollup's `InputPluginOption` union, which has no `push`. Rollup flattens nested plugin arrays.

### The spike's own recommendation

The spike recommended **implement, with caveats**; the decision at the top of this document overrides it. Effort is about half a day: the config and dependency change, the CLAUDE.md note, and one Vercel preview with a deliberately thrown server error checked in Sentry. Runtime risk is low. It is a build-only change, and the server bundle gets 26 KB smaller than today because it drops 57 debug-ID snippets that were never uploaded. Maintenance risk is medium: four undocumented or experimental internals, and a failure mode that is silent. Add the ~15-line build assertion described under Q5 so that failure becomes a failed Vercel build.

### Hygiene

- `git status --short` before: empty. After: empty.
- Untracked files created in the repo (`apps/web/vite.spike.config.ts`, `vite.proposal.config.ts`, `vite.proposal-run.config.ts`) and the git-ignored `apps/web/.vercel/` (absent before the spike) were deleted. The proposal config is reproduced above.
- `apps/web/.output` and `apps/web/dist` (git-ignored, both present before) were finally rebuilt with the tracked `vite.config.ts`.
- No package was installed, and `SENTRY_AUTH_TOKEN`/`SENTRY_ORG`/`SENTRY_PROJECT` were unset (`env -u`) for every build.
- Bundler-plugin telemetry was off (`telemetry: false`). The `sentry` CLI ran with `SENTRY_CLI_NO_TELEMETRY=1 DO_NOT_TRACK=1 SENTRY_CLI_NO_UPDATE_CHECK=1`, a throwaway config directory and `SENTRY_URL=http://127.0.0.1:9`. Nothing was uploaded.
