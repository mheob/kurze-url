// Command openapi writes the API's OpenAPI 3.1 document to stdout.
//
// It exists so the document can be produced without running the server.
// Registering an operation only describes it: Deps is never dereferenced until
// a request arrives, and authMiddleware returns a closure rather than touching
// the verifier, so a zero-value Deps yields the complete document with no
// database, no Redis and no JWT verifier.
//
// That matters twice over. CI regenerates the document to check it still
// matches the code — without that, adding an endpoint and forgetting to
// regenerate fails silently, leaving the generated client a version behind.
// And packages/api-client can be rebuilt by anyone without a Go toolchain or a
// running instance.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/mheob/kurze-url/apps/api/internal/api"
)

func main() {
	if err := run(os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "openapi:", err)
		os.Exit(1)
	}
}

func run(out io.Writer) error {
	// The mux is only a registration target; no request will ever reach it.
	humaAPI := humago.New(http.NewServeMux(), api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	encoded, err := humaAPI.OpenAPI().MarshalJSON()
	if err != nil {
		return fmt.Errorf("marshal the document: %w", err)
	}

	// Indented, because this document is committed and read as a diff. Huma
	// marshals it compactly, which would make every change one enormous line.
	var indented bytes.Buffer
	if err := json.Indent(&indented, encoded, "", "  "); err != nil {
		return fmt.Errorf("indent the document: %w", err)
	}
	indented.WriteByte('\n')

	if _, err := out.Write(indented.Bytes()); err != nil {
		return fmt.Errorf("write the document: %w", err)
	}
	return nil
}
