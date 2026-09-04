import { defineConfig } from '@hey-api/openapi-ts';

// The document is generated from the Go handlers by `apps/api/cmd/openapi`, so
// this reads a committed file rather than a running server: codegen needs no
// database, no Redis and no instance to talk to.
//
// The output is committed too, and oxfmt formats it like everything else —
// `pnpm generate:api` regenerates and then formats, so the tree is stable and
// `format:check` stays green.
export default defineConfig({
	input: '../../apps/api/openapi.json',
	output: {
		path: './src/generated',
	},
});
