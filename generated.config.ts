/**
 * Files produced by a generator, listed once and shared by oxlint and oxfmt.
 *
 * Both tools must agree on this set. When they disagreed, `routeTree.gen.ts`
 * was formatted by oxfmt and rewritten unformatted by every `vite build`, so
 * the working tree went dirty on each build and two implementers reverted it
 * by hand before anyone noticed the cause.
 *
 * Linting generated output reports on the generator's style, which nobody can
 * act on without editing a file that is overwritten on the next run. The same
 * argument applies to formatting it: the generator owns its output.
 */
export const generatedFiles = [
	// Written by @hey-api/openapi-ts from apps/api/openapi.json, including its
	// vendored request runtime.
	'packages/api-client/src/generated/**',
	// Written by TanStack Router (`tsr generate` and the Vite plugin) from the
	// files under apps/web/src/routes/. Its own header says not to edit it.
	'apps/web/src/routeTree.gen.ts',
];
