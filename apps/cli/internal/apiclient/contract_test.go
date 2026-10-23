package apiclient_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// specPath is the API's OpenAPI document, relative to this package. It is a
// variable and not a constant only so that a probe can aim the whole test at
// a doctored copy without touching the real file.
var specPath = filepath.FromSlash("../../../api/openapi.json")

// This file is what lets the client be written by hand. The CLI is a separate
// Go module that imports nothing from the backend, so nothing at compile time
// ties a JSON name or a query parameter here to the API. The spec is the one
// artifact both sides already agree on, and `ci-cli.yml` runs this suite
// whenever it changes, so drift is a red build instead of a user's 422.

// openAPI is the slice of an OpenAPI 3.1 document the contract needs.
type openAPI struct {
	Paths      map[string]pathItem `json:"paths"`
	Components struct {
		Schemas map[string]*schema `json:"schemas"`
	} `json:"components"`
}

// pathItem maps a lower-case HTTP method to its operation. It decodes only
// the method keys, so a path-level "parameters" array cannot break it.
type pathItem map[string]*operationSpec

// UnmarshalJSON keeps the HTTP methods and drops every other key.
func (p *pathItem) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*p = pathItem{}
	for _, method := range []string{"get", "put", "post", "delete", "patch", "head", "options", "trace"} {
		msg, ok := raw[method]
		if !ok {
			continue
		}
		var op operationSpec
		if err := json.Unmarshal(msg, &op); err != nil {
			return err
		}
		(*p)[method] = &op
	}
	return nil
}

// operationSpec is one operation: what it takes and what it answers.
type operationSpec struct {
	Parameters  []parameterSpec `json:"parameters"`
	RequestBody *struct {
		Content map[string]mediaType `json:"content"`
	} `json:"requestBody"`
	Responses map[string]struct {
		Content map[string]mediaType `json:"content"`
	} `json:"responses"`
}

// parameterSpec is one path, query or header parameter.
type parameterSpec struct {
	Name string `json:"name"`
	In   string `json:"in"`
}

// mediaType holds the schema of one content type.
type mediaType struct {
	Schema *schema `json:"schema"`
}

// schema is a JSON Schema as far as the contract reads it.
type schema struct {
	Ref        string             `json:"$ref"`
	Type       schemaTypes        `json:"type"`
	Properties map[string]*schema `json:"properties"`
	Items      *schema            `json:"items"`
}

// schemaTypes is a schema's "type", which OpenAPI 3.1 allows to be a string
// or a list of them (["array", "null"] for a nullable list).
type schemaTypes []string

// UnmarshalJSON accepts both spellings.
func (s *schemaTypes) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*s = schemaTypes{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

const schemaRefPrefix = "#/components/schemas/"

// resolve follows $ref until it reaches a schema with a body and also returns
// the last name it passed, which is what a failure message calls the schema.
func (o *openAPI) resolve(s *schema) (*schema, string) {
	name := ""
	for s != nil && s.Ref != "" {
		name = strings.TrimPrefix(s.Ref, schemaRefPrefix)
		s = o.Components.Schemas[name]
	}
	return s, name
}

// loadSpec reads and decodes the spec at specPath.
func loadSpec(t *testing.T) *openAPI {
	t.Helper()
	data, err := os.ReadFile(specPath)
	require.NoError(t, err, "the contract test needs the API's spec; is the checkout complete?")
	var spec openAPI
	require.NoError(t, json.Unmarshal(data, &spec))
	return &spec
}

// clientOperation is one API operation the client uses, spelled out in one
// place so that the spec check and the wire check read the same table.
type clientOperation struct {
	// name is the Client method, so a failure points at what to fix.
	name   string
	method string
	path   string
	// query lists every query parameter the client can send on this operation.
	query []string
	// request is a value of the JSON body's Go type, nil when there is no body.
	request any
	// status is the success status the spec documents for the operation.
	status string
	// response is a value of the JSON response's Go type; nil for the QR
	// operation, whose 200 is an image with no JSON schema.
	response any
	// media lists the content types the 200 must offer when response is nil.
	media []string
	// call runs the Client method with every optional input filled in.
	call func(ctx context.Context, c *apiclient.Client) error
}

// label names an operation in a failure.
func (op clientOperation) label() string {
	return fmt.Sprintf("%s (%s %s)", op.name, op.method, op.path)
}

// clientOperations is every operation the client calls. A new Client method
// is added here, and a method missing from this table is caught by
// TestEveryClientMethodIsInTheContractTable.
var clientOperations = []clientOperation{
	{
		name: "Me", method: http.MethodGet, path: apiclient.PathMe,
		status: "200", response: apiclient.Me{},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.Me(ctx)
			return err
		},
	},
	{
		name: "Folders", method: http.MethodGet, path: apiclient.PathTeamFolders,
		query:  []string{apiclient.QueryPage, apiclient.QueryPerPage},
		status: "200", response: apiclient.Page[apiclient.Folder]{},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.Folders(ctx, "team-1")
			return err
		},
	},
	{
		name: "Domains", method: http.MethodGet, path: apiclient.PathTeamDomains,
		query:  []string{apiclient.QueryPage, apiclient.QueryPerPage},
		status: "200", response: apiclient.Page[apiclient.Domain]{},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.Domains(ctx, "team-1")
			return err
		},
	},
	{
		name: "CreateLink", method: http.MethodPost, path: apiclient.PathTeamLinks,
		request: apiclient.CreateLinkRequest{},
		status:  "201", response: apiclient.Link{},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.CreateLink(ctx, "team-1", apiclient.CreateLinkRequest{
				DestinationURL: "https://example.org/", Slug: "s", FolderID: "f", DomainID: "d",
			})
			return err
		},
	},
	{
		name: "ListLinks", method: http.MethodGet, path: apiclient.PathTeamLinks,
		query: []string{
			apiclient.QueryQ, apiclient.QueryFolderID, apiclient.QuerySort,
			apiclient.QueryPage, apiclient.QueryPerPage,
		},
		status: "200", response: apiclient.Page[apiclient.Link]{},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.ListLinks(ctx, "team-1", apiclient.ListLinksQuery{
				Q: "q", FolderID: "f", Sort: "created_at", Page: 1, PerPage: 1,
			})
			return err
		},
	},
	{
		name: "LinkStats", method: http.MethodGet, path: apiclient.PathLinkStats,
		query:  []string{apiclient.QueryFrom, apiclient.QueryTo},
		status: "200", response: apiclient.LinkStats{},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.LinkStats(ctx, "link-1", "2026-10-01", "2026-10-02")
			return err
		},
	},
	{
		name: "LinkQR", method: http.MethodGet, path: apiclient.PathLinkQR,
		query:  []string{apiclient.QueryFormat, apiclient.QuerySize, apiclient.QueryFG, apiclient.QueryBG},
		status: "200", media: []string{"image/png", "image/svg+xml"},
		call: func(ctx context.Context, c *apiclient.Client) error {
			_, err := c.LinkQR(ctx, "link-1", apiclient.QRQuery{Format: "png", Size: 64, FG: "000000", BG: "ffffff"})
			return err
		},
	},
}

// checker collects every way the client disagrees with the spec, so one run
// reports all of them rather than the first.
type checker struct {
	spec       *openAPI
	violations []string
}

// report records one violation under the operation or area it belongs to.
func (c *checker) report(label, format string, args ...any) {
	c.violations = append(c.violations, label+": "+fmt.Sprintf(format, args...))
}

// checkContract compares the whole client against spec and returns what is
// wrong; an empty result means they agree.
func checkContract(spec *openAPI) []string {
	c := &checker{spec: spec}
	for _, op := range clientOperations {
		c.checkOperation(op)
	}
	c.checkProblemShape()
	return c.violations
}

// checkOperation verifies one operation: that it exists, that every query
// parameter the client sends is declared, and that the request and response
// types name only properties the schemas have.
func (c *checker) checkOperation(op clientOperation) {
	label := op.label()
	item, ok := c.spec.Paths[op.path]
	if !ok {
		c.report(label, "the spec has no path %q", op.path)
		return
	}
	decl, ok := item[strings.ToLower(op.method)]
	if !ok {
		c.report(label, "the spec declares no %s for path %q", op.method, op.path)
		return
	}

	declared := map[string]bool{}
	for _, p := range decl.Parameters {
		if p.In == "query" {
			declared[p.Name] = true
		}
	}
	for _, q := range op.query {
		if !declared[q] {
			c.report(label, "query parameter %q is not declared by the spec", q)
		}
	}

	if op.request != nil {
		if decl.RequestBody == nil || decl.RequestBody.Content["application/json"].Schema == nil {
			c.report(label, "the spec declares no JSON request body")
		} else {
			c.walk(label+" request body", reflect.TypeOf(op.request), decl.RequestBody.Content["application/json"].Schema)
		}
	}

	success := decl.Responses[op.status]
	if op.response != nil {
		if body := success.Content["application/json"].Schema; body == nil {
			c.report(label, "the spec declares no JSON body for the %s response", op.status)
		} else {
			c.walk(label+" response", reflect.TypeOf(op.response), body)
		}
	}
	for _, media := range op.media {
		if _, ok := success.Content[media]; !ok {
			c.report(label, "the %s response does not offer %q", op.status, media)
		}
	}

	// Every failure is decoded as a problem document, so every operation the
	// client calls must promise one.
	problem := decl.Responses["default"].Content["application/problem+json"].Schema
	if _, name := c.spec.resolve(problem); name != "ErrorModel" {
		c.report(label, "the default response is not application/problem+json with ErrorModel (got %q)", name)
	}
}

// checkProblemShape verifies that ProblemError and ProblemDetail decode what
// the spec's ErrorModel and ErrorDetail say the API sends.
func (c *checker) checkProblemShape() {
	model := &schema{Ref: schemaRefPrefix + "ErrorModel"}
	c.walk("ProblemError", reflect.TypeFor[apiclient.ProblemError](), model)
}

// walk compares a Go type with a schema. Every JSON name the type uses must
// be a property of the schema, recursing through struct fields and slices,
// and a scalar's kind must be one the schema allows. The location in where
// ends up in the message, so a failure names the operation and the exact
// field.
func (c *checker) walk(where string, t reflect.Type, s *schema) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	target, name := c.spec.resolve(s)
	if target == nil {
		c.report(where, "the schema %q cannot be resolved", name)
		return
	}
	if name == "" {
		name = "(inline)"
	}

	switch t.Kind() {
	case reflect.Struct:
		for i := range t.NumField() {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			jsonName := jsonNameOf(field)
			if jsonName == "-" {
				continue
			}
			prop, ok := target.Properties[jsonName]
			if !ok {
				c.report(where, "schema %s has no property %q (Go field %s.%s)", name, jsonName, t.Name(), field.Name)
				continue
			}
			c.walk(where+"."+jsonName, field.Type, prop)
		}
	case reflect.Slice:
		if !slices.Contains(target.Type, "array") || target.Items == nil {
			c.report(where, "schema %s is not an array, but the Go type is %s", name, t)
			return
		}
		c.walk(where+"[]", t.Elem(), target.Items)
	case reflect.String:
		c.expectKind(where, name, target, "string", t)
	case reflect.Bool:
		c.expectKind(where, name, target, "boolean", t)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		c.expectKind(where, name, target, "integer", t)
	case reflect.Interface:
		// A field declared `any` accepts whatever the schema says.
	default:
		c.report(where, "the contract test does not know how to compare the Go type %s", t)
	}
}

// expectKind reports a scalar whose JSON type the schema does not allow. A
// schema that states no type at all (ErrorDetail's "value") allows anything.
func (c *checker) expectKind(where, name string, s *schema, want string, t reflect.Type) {
	if len(s.Type) == 0 || slices.Contains(s.Type, want) {
		return
	}
	c.report(where, "the schema %s says %v, but the Go type %s needs %q", name, []string(s.Type), t, want)
}

// jsonNameOf returns the name encoding/json uses for a struct field.
func jsonNameOf(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

// jsonNamesOf lists the JSON names a struct type encodes, sorted.
func jsonNamesOf(t reflect.Type) []string {
	var names []string
	for i := range t.NumField() {
		if f := t.Field(i); f.IsExported() && jsonNameOf(f) != "-" {
			names = append(names, jsonNameOf(f))
		}
	}
	sort.Strings(names)
	return names
}

func TestContractMatchesOpenAPI(t *testing.T) {
	spec := loadSpec(t)

	violations := checkContract(spec)

	for _, v := range violations {
		t.Error(v)
	}
}

// TestContractCheckNoticesDrift keeps the check above honest: a contract test
// that cannot fail is worse than none. Each case damages a freshly loaded
// spec the way an API change would and expects the report to name both the
// operation and what is missing.
func TestContractCheckNoticesDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(spec *openAPI)
		want   []string
	}{
		{
			name: "a renamed response property",
			damage: func(spec *openAPI) {
				delete(spec.Components.Schemas["Link"].Properties, "short_url")
			},
			want: []string{
				`CreateLink (POST /v1/teams/{team_id}/links) response: schema Link has no property "short_url"`,
				`ListLinks (GET /v1/teams/{team_id}/links) response.items[]: schema Link has no property "short_url"`,
			},
		},
		{
			name: "a renamed request property",
			damage: func(spec *openAPI) {
				delete(spec.Components.Schemas["CreateLinkInputBody"].Properties, "destination_url")
			},
			want: []string{
				`CreateLink (POST /v1/teams/{team_id}/links) request body: schema CreateLinkInputBody has no property "destination_url"`,
			},
		},
		{
			name: "a dropped query parameter",
			damage: func(spec *openAPI) {
				op := spec.Paths[apiclient.PathTeamLinks]["get"]
				op.Parameters = slices.DeleteFunc(op.Parameters, func(p parameterSpec) bool { return p.Name == "folder_id" })
			},
			want: []string{"ListLinks (GET /v1/teams/{team_id}/links)", `query parameter "folder_id"`},
		},
		{
			name: "a moved path",
			damage: func(spec *openAPI) {
				delete(spec.Paths, apiclient.PathLinkStats)
			},
			want: []string{"LinkStats (GET /v1/links/{link_id}/stats)", "no path"},
		},
		{
			name: "a removed method",
			damage: func(spec *openAPI) {
				delete(spec.Paths[apiclient.PathTeamLinks], "post")
			},
			want: []string{"CreateLink (POST /v1/teams/{team_id}/links)", "no POST"},
		},
		{
			name: "a property that changed type",
			damage: func(spec *openAPI) {
				spec.Components.Schemas["Link"].Properties["destination_url"].Type = schemaTypes{"integer"}
			},
			want: []string{"response.destination_url", "needs \"string\""},
		},
		{
			name: "a media type that disappeared",
			damage: func(spec *openAPI) {
				delete(spec.Paths[apiclient.PathLinkQR]["get"].Responses["200"].Content, "image/png")
			},
			want: []string{"LinkQR (GET /v1/links/{link_id}/qr)", `"image/png"`},
		},
		{
			name: "a problem document that changed shape",
			damage: func(spec *openAPI) {
				delete(spec.Components.Schemas["ErrorDetail"].Properties, "location")
			},
			want: []string{`ProblemError.errors[]: schema ErrorDetail has no property "location"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := loadSpec(t)
			require.Empty(t, checkContract(spec), "the real spec must pass before it is damaged")

			tc.damage(spec)
			joined := strings.Join(checkContract(spec), "\n")

			require.NotEmpty(t, joined, "damage went unnoticed")
			for _, want := range tc.want {
				require.Contains(t, joined, want)
			}
		})
	}
}

// TestTheContractTableDescribesWhatTheClientSends closes the gap the table
// leaves open: TestContractMatchesOpenAPI trusts the table, so the table is
// itself checked against the wire. Each client method is called with every
// optional input filled in, and what reaches the server has to be exactly
// the method, path, query names and body names the table claims.
func TestTheContractTableDescribesWhatTheClientSends(t *testing.T) {
	for _, op := range clientOperations {
		t.Run(op.name, func(t *testing.T) {
			status, err := strconv.Atoi(op.status)
			require.NoError(t, err)
			f := newFakeAPI(t, func(_ int, _ seenRequest, w http.ResponseWriter) {
				writeJSON(w, status, `{}`)
			})

			require.NoError(t, op.call(t.Context(), newClient(f, &fakeTokens{})))

			got := f.requests()
			require.NotEmpty(t, got)
			first := got[0]
			require.Equal(t, op.method, first.Method)
			require.Equal(t, expandPath(op.path), first.Path)
			queryNames := make([]string, 0, len(first.Query))
			for name := range first.Query {
				queryNames = append(queryNames, name)
			}
			require.ElementsMatch(t, op.query, queryNames,
				"%s sends different query names than the table lists", op.name)
			if op.request != nil {
				var body map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(first.Body, &body))
				bodyNames := make([]string, 0, len(body))
				for name := range body {
					bodyNames = append(bodyNames, name)
				}
				require.ElementsMatch(t, jsonNamesOf(reflect.TypeOf(op.request)), bodyNames)
			} else {
				require.Empty(t, first.Body)
			}
		})
	}
}

// TestEveryClientMethodIsInTheContractTable fails when someone adds an API
// call to Client and forgets the table, which would leave that call outside
// every check in this file.
func TestEveryClientMethodIsInTheContractTable(t *testing.T) {
	var methods []string
	client := reflect.TypeFor[*apiclient.Client]()
	for i := range client.NumMethod() {
		methods = append(methods, client.Method(i).Name)
	}
	var tabled []string
	for _, op := range clientOperations {
		tabled = append(tabled, op.name)
	}

	require.ElementsMatch(t, methods, tabled)
}

// expandPath fills the template's placeholders with the IDs the table's
// calls use.
func expandPath(template string) string {
	return strings.NewReplacer("{team_id}", "team-1", "{link_id}", "link-1").Replace(template)
}
