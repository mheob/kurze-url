package api_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// matrixCase is one row of the role permission matrix in the plan and spec.
// path is rendered against the fixture: {team} and {member} are substituted.
type matrixCase struct {
	operationID string
	method      string
	path        string
	body        any
	minRole     authz.Role
}

// teamScopedCases is the enforced contract. Every team-scoped operation must
// appear here, and TestEveryOperationIsAccountedFor fails the build if a new
// one is added without a row.
var teamScopedCases = []matrixCase{
	{"get-team", http.MethodGet, "/v1/teams/{team}", nil, authz.RoleViewer},
	{"update-team", http.MethodPatch, "/v1/teams/{team}",
		map[string]string{"name": "Umbenannt"}, authz.RoleAdmin},
	{"list-team-members", http.MethodGet, "/v1/teams/{team}/members", nil, authz.RoleViewer},
	{"add-team-member", http.MethodPost, "/v1/teams/{team}/members",
		map[string]string{"email": "matrix@verein.test", "role": "viewer"}, authz.RoleAdmin},
	{"update-team-member", http.MethodPatch, "/v1/teams/{team}/members/{member}",
		map[string]string{"role": "editor"}, authz.RoleAdmin},
	{"remove-team-member", http.MethodDelete, "/v1/teams/{team}/members/{member}",
		nil, authz.RoleAdmin},
	{"list-audit-log", http.MethodGet, "/v1/teams/{team}/audit-log", nil, authz.RoleAdmin},
	{"create-link", http.MethodPost, "/v1/teams/{team}/links",
		map[string]string{"destination_url": "https://example.org/matrix"}, authz.RoleEditor},
}

// notTeamScoped names the authenticated operations that legitimately carry no
// scope embed. Adding to this list must be a deliberate edit.
var notTeamScoped = map[string]string{
	"get-me":      "session endpoint; scoped to the caller, not to a team",
	"create-team": "gated by the maintainer allowlist, not by a team role",
	"list-teams":  "returns only the caller's own memberships",
}

// publicOperations names the operations that legitimately need no bearer
// token at all. This list is intentionally small: adding to it must be a
// deliberate, reviewable choice, with a comment saying why the operation is
// genuinely public. A new operation is unauthenticated only on purpose here,
// never by omission.
var publicOperations = map[string]string{
	// Liveness probe. Must be reachable with no credentials, or uptime
	// monitoring and the load balancer's health check both break.
	"get-health": "liveness probe; must be reachable with no credentials",
}

func renderPath(f *tenancyFixture, template string) string {
	path := template
	path = strings.ReplaceAll(path, "{team}", f.teamID.String())
	path = strings.ReplaceAll(path, "{member}", f.members[authz.RoleViewer].id.String())
	return path
}

// TestRolePermissionMatrix crosses every team-scoped operation with every role
// plus a non-member, and asserts the documented outcome. A fresh fixture per
// pair keeps mutating cases independent.
func TestRolePermissionMatrix(t *testing.T) {
	roles := []authz.Role{authz.RoleViewer, authz.RoleEditor, authz.RoleAdmin, authz.RoleOwner}

	for _, tc := range teamScopedCases {
		for _, role := range roles {
			t.Run(tc.operationID+"/"+role.String(), func(t *testing.T) {
				f := newTenancyFixture(t)
				rec := f.do(t, f.members[role], tc.method, renderPath(f, tc.path), tc.body)

				if role.AtLeast(tc.minRole) {
					require.Less(t, rec.Code, 400,
						"%s must be allowed for %s; body: %s", tc.operationID, role, rec.Body.String())
					return
				}
				require.Equal(t, http.StatusForbidden, rec.Code,
					"%s must be refused for %s", tc.operationID, role)
			})
		}

		t.Run(tc.operationID+"/non-member", func(t *testing.T) {
			f := newTenancyFixture(t)
			rec := f.do(t, f.stranger, tc.method, renderPath(f, tc.path), tc.body)

			require.Equal(t, http.StatusNotFound, rec.Code,
				"%s must hide the team from a non-member", tc.operationID)
		})

		t.Run(tc.operationID+"/unauthenticated", func(t *testing.T) {
			f := newTenancyFixture(t)
			rec := f.do(t, testUser{}, tc.method, renderPath(f, tc.path), tc.body)

			require.Equal(t, http.StatusUnauthorized, rec.Code,
				"%s must require a bearer token", tc.operationID)
		})
	}
}

// operationInfo is the sliver of a registered huma.Operation the structural
// guard needs. Factored out as data (rather than working directly off
// *huma.Operation) so checkOperationCoverage can be exercised against a
// synthetic list in tests, without registering a throwaway operation into
// the real API surface.
type operationInfo struct {
	operationID string
	path        string
	bearer      bool
}

// collectOperations reads every operation Huma has registered off the
// generated OpenAPI document.
func collectOperations(humaAPI huma.API) []operationInfo {
	var infos []operationInfo
	for path, item := range humaAPI.OpenAPI().Paths {
		for _, operation := range operationsOf(item) {
			infos = append(infos, operationInfo{
				operationID: operation.OperationID,
				path:        path,
				bearer:      declaresBearer(operation),
			})
		}
	}
	return infos
}

// checkOperationCoverage is the structural guard. There is no RLS: an
// operation that reaches team data without a scope embed is a cross-tenant
// leak, and an operation reachable with no bearer token at all is worse
// still, since it needs no membership either. So every registered operation
// must be accounted for one of two ways: authenticated (bearerAuth declared)
// and covered by a matrix row or a notTeamScoped exemption; or explicitly
// named in publicOperations. An operation satisfying neither is unaccounted
// for — public by omission, not by a reviewed decision — and is reported.
//
// This is a plain function of an operation list, not a *testing.T-driven
// assertion, precisely so it can run against both the real /v1 surface and a
// synthetic list built inside a test.
func checkOperationCoverage(operations []operationInfo) []string {
	covered := map[string]bool{}
	for _, tc := range teamScopedCases {
		covered[tc.operationID] = true
	}

	var problems []string
	for _, op := range operations {
		if op.bearer {
			if covered[op.operationID] {
				continue
			}
			if _, ok := notTeamScoped[op.operationID]; ok {
				continue
			}
			problems = append(problems, fmt.Sprintf(
				"operation %q (%s) is authenticated but has no permission-matrix row; "+
					"add one to teamScopedCases, or name it in notTeamScoped with a reason",
				op.operationID, op.path))
			continue
		}

		if _, ok := publicOperations[op.operationID]; ok {
			continue
		}
		problems = append(problems, fmt.Sprintf(
			"operation %q (%s) declares no bearerAuth security and is not in "+
				"publicOperations; add Security: bearerAuth plus a permission-matrix "+
				"row (or a notTeamScoped exemption) if it touches team data, or add it "+
				"to publicOperations with a comment saying why it is genuinely public",
			op.operationID, op.path))
	}
	return problems
}

// TestEveryOperationIsAccountedFor is the structural guard run against the
// real /v1 surface. It asks, for every operation Huma has registered — not
// only the ones that already declare bearerAuth — whether it is either
// authenticated and in the matrix, or explicitly named as public. An
// operation satisfying neither fails the build: a new endpoint is
// unauthenticated only when its author made that call on purpose, never by
// forgetting both Security and a scope embed.
func TestEveryOperationIsAccountedFor(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	problems := checkOperationCoverage(collectOperations(humaAPI))
	require.Empty(t, problems, "unaccounted-for operations:\n%s", strings.Join(problems, "\n"))
}

// TestGuardFlagsAnOperationWithNoSecurityAndNoMatrixRow proves the guard
// catches the dangerous half of the failure mode: an operation registered
// with neither bearerAuth nor a matrix row is not a drift, it is silently
// public, and a wrong scope at least still demands a token and a membership.
func TestGuardFlagsAnOperationWithNoSecurityAndNoMatrixRow(t *testing.T) {
	synthetic := []operationInfo{
		{operationID: "sneaky-public-by-omission", path: "/v1/teams/{team_id}/sneaky", bearer: false},
	}

	problems := checkOperationCoverage(synthetic)

	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "sneaky-public-by-omission")
	require.Contains(t, problems[0], "publicOperations",
		"the failure message must tell the author how to fix it")
}

// TestGuardFlagsAnAuthenticatedOperationWithNoMatrixRow proves the other
// half still works: an operation that does declare bearerAuth but has no
// matrix row or exemption is still refused.
func TestGuardFlagsAnAuthenticatedOperationWithNoMatrixRow(t *testing.T) {
	synthetic := []operationInfo{
		{operationID: "sneaky-authenticated-endpoint", path: "/v1/teams/{team_id}/sneaky", bearer: true},
	}

	problems := checkOperationCoverage(synthetic)

	require.Len(t, problems, 1)
	require.Contains(t, problems[0], "sneaky-authenticated-endpoint")
	require.Contains(t, problems[0], "teamScopedCases",
		"the failure message must tell the author how to fix it")
}

// TestGuardAllowsCoveredAndPublicOperations proves the guard does not flag
// what it shouldn't: an operation with a real matrix row, and an operation
// named in publicOperations, both pass.
func TestGuardAllowsCoveredAndPublicOperations(t *testing.T) {
	synthetic := []operationInfo{
		{operationID: "get-team", path: "/v1/teams/{team_id}", bearer: true},
		{operationID: "get-health", path: "/v1/health", bearer: false},
	}

	require.Empty(t, checkOperationCoverage(synthetic))
}

func operationsOf(item *huma.PathItem) []*huma.Operation {
	all := []*huma.Operation{
		item.Get, item.Post, item.Put, item.Patch, item.Delete,
		item.Head, item.Options, item.Trace,
	}
	out := make([]*huma.Operation, 0, len(all))
	for _, operation := range all {
		if operation != nil {
			out = append(out, operation)
		}
	}
	return out
}

func declaresBearer(operation *huma.Operation) bool {
	for _, scheme := range operation.Security {
		if _, ok := scheme["bearerAuth"]; ok {
			return true
		}
	}
	return false
}

// TestOpenAPIExcludesTheRedirectSurface re-asserts plan 1's boundary now that
// many more operations are registered.
func TestOpenAPIExcludesTheRedirectSurface(t *testing.T) {
	router := chi.NewRouter()
	humaAPI := humachi.New(router, api.NewHumaConfig())
	api.Deps{}.RegisterV1(humaAPI)

	for path := range humaAPI.OpenAPI().Paths {
		require.NotContains(t, path, "{slug}",
			"the public redirect surface must never appear in the OpenAPI document")
	}
}
