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
	// Written by TanStack Start's Vite plugin from the files under
	// apps/web/src/routes/. Its own header says not to edit it.
	'apps/web/src/routeTree.gen.ts',
	// Written by `shadcn add`, from the primitive layer (`base-sera`) recorded
	// in apps/web/components.json. Regenerated wholesale on every `add`; never
	// hand-edited.
	'apps/web/src/components/ui/**',
	// Also `shadcn add` output — `components.json`'s aliases send hooks to
	// `@/hooks`, and the sidebar component brings this one with it. Named on
	// its own rather than as `apps/web/src/hooks/**`: there is no hand-written
	// hook in that directory yet, and a directory glob would exempt the first
	// one by accident instead of forcing a deliberate choice when it arrives.
	'apps/web/src/hooks/use-mobile.ts',
];
