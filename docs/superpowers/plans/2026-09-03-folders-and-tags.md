# Folders and Tags Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team can group its links into folders and label them with tags, find links by either, and none of it costs the redirect path anything.

**Architecture:** Two new team-scoped resources with the same shape as links: team-path routes for create and list, entity-path routes for update and delete authorized through per-entity scopes that resolve the entity to discover its team. Link writes gain a `folder_id` and a `tag_ids` array, both validated against the caller's team inside the write's own transaction. The link list gains two filters and returns each link's tags via one extra query per page.

**Tech Stack:** Go, chi + Huma v2, sqlc against Postgres (Supabase), pgx v5. Tests run `go test` against real Postgres and Redis.

**Spec:** `docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`

## Global Constraints

These bind every task. They are the project's rules, restated so you do not have to reconstruct them from `CLAUDE.md`.

- The tenant is called `team` in every identifier — tables, columns, Go types, API paths. Never `verein_id`. "Verein" appears only in user-facing German copy.
- `GET /<slug>` is the hot path. Nothing in this plan may cost it a single extra Redis command or database round trip.
- **There is no RLS.** Postgres enforces nothing about tenancy. Every query that touches team data filters by `team_id` in Go. A query without that filter is a data-leak bug, not a style issue.
- Never store a full IP address, anywhere, ever.
- Errors are Huma's default RFC 9457 `application/problem+json`. Do not build a custom error model.
- Pagination is offset/limit with the existing `api.Page[T]` envelope and `api.PageParams`, `per_page` capped at 100. Never pagination headers.
- Filtering uses flat, explicitly typed query parameters per endpoint. Not a generic `filter=field:op:value` scheme.
- Operations that need authentication declare `Security: []map[string][]string{{"bearerAuth": {}}}`.
- Migrations are owned by the Supabase CLI: `supabase migration new <name>`, then `supabase db push`. No golang-migrate, no Atlas, no hand-written migration files.
- Database access is sqlc-generated from raw SQL in `apps/api/internal/db/queries/`. Run `sqlc generate` from `apps/api` after editing any `.sql` file. No ORM.
- Go tests run against real Postgres and Redis, as plans 2 and 3 do. Mocks are for external HTTP only. No skips.
- Conventional Commits, checked in CI.
- Run `pnpm format`, `pnpm lint`, `go vet ./...` and `golangci-lint run` before each commit.

---

## File Structure

**Create:**

| Path | Responsibility |
| --- | --- |
| `supabase/migrations/<ts>_tag_name_case_insensitive.sql` | Replace `tag`'s unique constraint with a case-folding unique index. |
| `apps/api/internal/api/limits.go` | The three count caps, the name-length cap, and the one name-validation function both resources share. |
| `apps/api/internal/api/limits_test.go` | Unit tests for name validation. |
| `apps/api/internal/db/queries/folder.sql` | Every folder statement. |
| `apps/api/internal/db/queries/tag.sql` | Every tag and `link_tag` statement. |
| `apps/api/internal/authz/folder.go` | `FolderEditorScope` and its resolver. |
| `apps/api/internal/authz/folder_test.go` | Scope decision tests against fakes. |
| `apps/api/internal/authz/tag.go` | `TagEditorScope` and its resolver. |
| `apps/api/internal/authz/tag_test.go` | Scope decision tests against fakes. |
| `apps/api/internal/api/folders.go` | Folder endpoints. |
| `apps/api/internal/api/folders_test.go` | Folder endpoint tests. |
| `apps/api/internal/api/tags.go` | Tag endpoints. |
| `apps/api/internal/api/tags_test.go` | Tag endpoint tests. |
| `apps/api/internal/api/organization_isolation_test.go` | Cross-team isolation for folders, tags and link references. |

**Modify:**

| Path | Change |
| --- | --- |
| `apps/api/internal/authz/scope.go` | `claimsUserID` returns an ok flag; `resolveMembership` enforces its own precondition. |
| `apps/api/internal/audit/audit.go` | Six new actions, two new entity types. |
| `apps/api/internal/api/links.go` | `folder_id` and `tag_ids` on create and update; two list filters; `folder_id` and `tags` on the representation. |
| `apps/api/internal/api/v1.go` | Register the folder and tag routes and install their resolvers. |
| `apps/api/internal/api/matrix_test.go` | Eight new rows. |
| `apps/api/internal/db/queries/link_crud.sql` | `folder_id` on the link write and read queries; two new filters on the list. |
| `docs/planning/05-database-schema.md` | Amend the `parent_folder_id` nesting note. |
| `docs/planning/06-api-design.md` | Record how tags attach to a link. |

Folders and tags get separate files rather than one `organization.go`. They are separate resources with separate queries, scopes and audit actions; the only thing they share is the validation in `limits.go`, and that is where the sharing belongs.

---

### Task 1: Case-insensitive tag names

**Files:**

- Create: `supabase/migrations/<timestamp>_tag_name_case_insensitive.sql`

**Interfaces:**

- Consumes: nothing.
- Produces: the constraint `tag_team_id_name_key` no longer exists; the unique index `tag_team_id_name_lower_idx` on `(team_id, lower(name))` does. Task 7's `CreateTag` and `UpdateTag` rely on it to raise a unique violation.

- [ ] **Step 1: Confirm the constraint's real name**

Do not trust the spec's assumption. Run:

```bash
docker exec supabase_db_kurze-url psql -U postgres -d postgres -tAc \
  "select conname, contype from pg_constraint where conrelid='public.tag'::regclass order by contype;"
```

Expected output includes `tag_team_id_name_key|u`. If the name differs, use the real one in Step 2 and note it in the commit message.

- [ ] **Step 2: Create the migration**

```bash
cd /Users/ab/dev/customer/itsb/kurze-url
supabase migration new tag_name_case_insensitive
```

Write into the generated file:

```sql
-- Tag names are unique per team case-insensitively. German capitalizes every
-- noun, so a team's tags are capitalized words a hurried user will sometimes
-- type lowercase; "Sommerfest" and "sommerfest" as two tags is a filter list
-- that quietly rots. The name is still STORED exactly as typed, because
-- "sommerfest" rendered in a German UI reads as a typo — the display value and
-- the uniqueness key are different things.
--
-- The index holds the invariant rather than a strings.ToLower in Go: there is
-- no RLS on this project, so Go already carries every tenancy guarantee, and a
-- normalization call is one line that a later code path can forget to copy. A
-- unique index cannot be forgotten.
alter table tag drop constraint tag_team_id_name_key;

create unique index tag_team_id_name_lower_idx on tag (team_id, lower(name));
```

- [ ] **Step 3: Apply it**

Run: `supabase db push` Expected: the migration applies with no error.

- [ ] **Step 4: Verify both halves took effect**

```bash
docker exec supabase_db_kurze-url psql -U postgres -d postgres -tAc \
  "select indexname from pg_indexes where tablename='tag';"
docker exec supabase_db_kurze-url psql -U postgres -d postgres -tAc \
  "select conname from pg_constraint where conrelid='public.tag'::regclass and contype='u';"
```

Expected: the first lists `tag_team_id_name_lower_idx`; the second is empty.

- [ ] **Step 5: Prove the index actually folds case**

```bash
docker exec supabase_db_kurze-url psql -U postgres -d postgres -c "
begin;
insert into team (id, name) values ('11111111-1111-1111-1111-111111111111', 'Probe');
insert into tag (team_id, name) values ('11111111-1111-1111-1111-111111111111', 'Sommerfest');
insert into tag (team_id, name) values ('11111111-1111-1111-1111-111111111111', 'SOMMERFEST');
rollback;"
```

Expected: the second insert fails with `duplicate key value violates unique constraint "tag_team_id_name_lower_idx"`. The transaction rolls back either way, so nothing is left behind.

- [ ] **Step 6: Commit**

```bash
git add supabase/migrations
git commit -m "feat(db): fold case on tag name uniqueness"
```

---

### Task 2: Shared limits and name validation

**Files:**

- Create: `apps/api/internal/api/limits.go`
- Create: `apps/api/internal/api/limits_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `maxFoldersPerTeam = 100`, `maxTagsPerTeam = 200`, `maxTagsPerLink = 10`, `maxResourceNameLength = 60`, and `validateResourceName(raw string) (string, error)` returning the trimmed name. Tasks 6, 9, 10 and 11 all call it.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/api/limits_test.go`:

```go
package api

import (
	"strings"
	"testing"
)

func TestValidateResourceNameTrims(t *testing.T) {
	got, err := validateResourceName("  Sommerfest 2026\t")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "Sommerfest 2026" {
		t.Fatalf("got %q, want %q", got, "Sommerfest 2026")
	}
}

func TestValidateResourceNameKeepsCaseAndUnicode(t *testing.T) {
	// The stored value is the display value: umlauts, emoji and inner spaces
	// all survive, and nothing is lowercased.
	for _, name := range []string{"Grüße", "Presse & PR", "Sommerfest 🎉", "ÖPNV"} {
		got, err := validateResourceName(name)
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", name, err)
		}
		if got != name {
			t.Fatalf("got %q, want %q unchanged", got, name)
		}
	}
}

func TestValidateResourceNameRejectsEmpty(t *testing.T) {
	for _, name := range []string{"", "   ", "\t\n"} {
		if _, err := validateResourceName(name); err == nil {
			t.Fatalf("%q: want an error, got none", name)
		}
	}
}

func TestValidateResourceNameCountsCharactersNotBytes(t *testing.T) {
	// 60 umlauts are 120 bytes. A byte-length check would reject a name that
	// is exactly at the limit, which is a bug a German-language project would
	// hit on its first real tag.
	atLimit := strings.Repeat("ü", maxResourceNameLength)
	if _, err := validateResourceName(atLimit); err != nil {
		t.Fatalf("a name of exactly %d characters must be accepted: %v", maxResourceNameLength, err)
	}

	overLimit := strings.Repeat("ü", maxResourceNameLength+1)
	if _, err := validateResourceName(overLimit); err == nil {
		t.Fatalf("a name of %d characters must be rejected", maxResourceNameLength+1)
	}
}

func TestValidateResourceNameMeasuresAfterTrimming(t *testing.T) {
	padded := "  " + strings.Repeat("a", maxResourceNameLength) + "  "
	if _, err := validateResourceName(padded); err != nil {
		t.Fatalf("whitespace must not count toward the limit: %v", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/api/ -run TestValidateResourceName -v` Expected: FAIL — `undefined: validateResourceName`, `undefined: maxResourceNameLength`.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/api/limits.go`:

```go
package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Count caps on the resources a team names for itself.
//
// These are not rate limits — the per-user write rate limit still applies on
// top. They exist because the Supabase free tier is 500 MB and has no backups
// at all, which makes unbounded row growth the one failure mode with no
// recovery path. A rate limit makes bulk creation slow; a cap makes it
// impossible.
//
// The numbers are generous for a Verein by roughly an order of magnitude.
// Raising one is a one-line change here.
const (
	maxFoldersPerTeam = 100
	maxTagsPerTeam    = 200
	maxTagsPerLink    = 10
)

// maxResourceNameLength bounds a folder or tag name in characters, not bytes.
// Sixty fits a filter chip and a table column without truncation and holds the
// longest realistic German compound. The cap exists mainly because name is
// `text` in Postgres: the count caps above bound how many rows a team creates,
// and this bounds how large one gets.
const maxResourceNameLength = 60

// ErrNameEmpty and ErrNameTooLong are returned by validateResourceName.
// Handlers turn them into 422s; they are values rather than strings so a test
// can assert which rule fired.
var (
	ErrNameEmpty   = errors.New("name must not be empty")
	ErrNameTooLong = fmt.Errorf("name must be at most %d characters", maxResourceNameLength)
)

// validateResourceName is the one naming rule folders and tags share: trimmed
// of surrounding whitespace, non-empty, and at most maxResourceNameLength
// characters. It returns the trimmed name, which is what gets stored — case,
// umlauts, inner spaces and emoji all survive untouched.
//
// Length is counted in runes. Sixty umlauts are 120 bytes, so a byte-length
// check would reject a name that is exactly at the limit.
func validateResourceName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", ErrNameEmpty
	}
	if utf8.RuneCountInString(name) > maxResourceNameLength {
		return "", ErrNameTooLong
	}
	return name, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -run TestValidateResourceName -v` Expected: PASS, all five.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/api/limits.go apps/api/internal/api/limits_test.go
git commit -m "feat(api): add shared name validation and count caps"
```

---

### Task 3: Enforce the resolveMembership precondition

**Files:**

- Modify: `apps/api/internal/authz/scope.go`
- Modify: `apps/api/internal/authz/scope_test.go`

**Interfaces:**

- Consumes: nothing.
- Produces: `resolveMembership` now returns 401 rather than querying for the nil user when claims are absent. Tasks 5 and 8 add the third and fourth callers and rely on this.

**Why now.** `resolveMembership` documents a precondition it does not enforce: callers must check `auth.ClaimsFromContext` first. Both current callers do. `claimsUserID` discards the ok flag, so a caller that forgot would query `team_member` for `uuid.Nil`, get `ErrNotMember`, and answer 404 — when the honest answer is 401. This plan adds four more callers, which is where a documented precondition stops being safe.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/authz/membership_internal_test.go`.

**This is the package's only in-package test, and that is deliberate.** Every other test file in `internal/authz` is `package authz_test`, driving the scopes through `humatest` the way Huma runs them. That harness cannot reach `resolveMembership`, because every exported path to it checks claims earlier and returns 401 first — a black-box test would pass without the guard existing. Testing an unexported guard that no exported path can reach is exactly what an in-package test is for. Keep this file to that one purpose; new scope tests still go in `authz_test`.

```go
package authz

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
)

// refusingResolver fails the test if it is ever consulted.
type refusingResolver struct{ t *testing.T }

func (r refusingResolver) Membership(
	context.Context, uuid.UUID, uuid.UUID,
) (Membership, error) {
	r.t.Fatal("resolveMembership queried membership without verified claims")
	return Membership{}, nil
}

func TestResolveMembershipRefusesWithoutClaims(t *testing.T) {
	// The precondition is that callers check claims first. This asserts what
	// happens when one does not: 401, not a membership lookup for the nil user
	// that would surface as a misleading 404.
	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))

	var got []error
	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/probe",
	}, func(ctx context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
	})

	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		// No auth.WithClaims: the caller is unauthenticated. A resolver IS
		// installed, so reaching it would be the bug.
		inner := WithResolver(ctx.Context(), refusingResolver{t: t})
		var out Membership
		got = resolveMembership(huma.WithContext(ctx, inner),
			uuid.New(), RoleViewer, "team not found", &out)
		next(ctx)
	})

	api.Get("/probe")

	if len(got) != 1 {
		t.Fatalf("got %d errors, want 1", len(got))
	}
	var status huma.StatusError
	if !errors.As(got[0], &status) || status.GetStatus() != http.StatusUnauthorized {
		t.Fatalf("got %v, want 401", got[0])
	}
}

var _ = httptest.NewRequest // keep the import if the harness above needs it; drop otherwise
```

If `humatest` proves awkward for constructing a bare `huma.Context` here, any construction that yields one works — the assertion is what matters: with no claims installed, `resolveMembership` returns exactly one 401 and never consults the resolver. Drop the trailing `var _ =` line and its import if unused.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/authz/ -run TestResolveMembershipRefusesWithoutClaims -v` Expected: FAIL — the test's `t.Fatal` fires, because `resolveMembership` reaches the resolver.

- [ ] **Step 3: Make the precondition enforceable**

In `apps/api/internal/authz/scope.go`, change `claimsUserID` to report whether claims were present:

```go
// claimsUserID reads the caller's user ID from the verified claims, reporting
// whether any were present. Both resolveScope's callers and the entity scopes
// check claims themselves before they get here; the ok flag is what lets
// resolveMembership enforce that rather than trust it.
func claimsUserID(ctx huma.Context) (uuid.UUID, bool) {
	claims, ok := auth.ClaimsFromContext(ctx.Context())
	if !ok {
		return uuid.Nil, false
	}
	return claims.UserID, true
}
```

- [ ] **Step 4: Enforce it in resolveMembership**

Replace the first lines of `resolveMembership`'s body, above the resolver lookup:

```go
	// The precondition — callers check claims first — is enforced here rather
	// than documented. Without it a caller that forgot would look up membership
	// for uuid.Nil, get ErrNotMember and answer 404: the wrong defect, and one
	// that hides an authentication bug behind a plausible-looking response.
	userID, ok := claimsUserID(ctx)
	if !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	resolver, found := resolverFromContext(ctx.Context())
	if !found {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know whether this caller belongs to the team.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	membership, err := resolver.Membership(ctx.Context(), teamID, userID)
```

Also update the doc comment above `resolveMembership`, replacing the sentence that describes the precondition as the caller's job:

```go
// resolveMembership is the whole authorization decision, shared by the
// team-path scopes and the entity scopes. notFound is the message used when
// the caller is not a member: the wording differs per route so a 404 never
// says "team not found" on a link route.
//
// It checks for verified claims itself. Callers check too — earlier, so that an
// unauthenticated request with a malformed path parameter is a 401 rather than
// a 422 — but correctness does not depend on their doing so.
//
// The membership travels out through out rather than the context because
// Resolve returns only errors — it cannot replace the context the handler
// receives — but it can mutate the input struct it is part of.
```

- [ ] **Step 5: Run the whole authz package**

Run: `cd apps/api && go test ./internal/authz/ -v` Expected: PASS, including the new test and every existing one. The callers' own early claims checks still fire first, so no existing status code changes.

- [ ] **Step 6: Run the API package too**

Run: `cd apps/api && go test ./internal/api/` Expected: PASS. This is the check that no status code moved — the permission matrix would catch it.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal/authz/scope.go apps/api/internal/authz/scope_test.go
git commit -m "fix(api): enforce the resolveMembership claims precondition"
```

---

### Task 4: Folder queries

**Files:**

- Create: `apps/api/internal/db/queries/folder.sql`

**Interfaces:**

- Consumes: nothing.
- Produces: `GetFolderScope`, `GetFolderInTeam`, `CreateFolder`, `ListFoldersForTeam`, `CountFoldersForTeam`, `UpdateFolder`, `DeleteFolder`, `CountLinksInFolder` on `*db.Queries`. Tasks 5, 6, 10 and 11 call them.

- [ ] **Step 1: Write the queries**

Create `apps/api/internal/db/queries/folder.sql`:

```sql
-- Folder CRUD. There is no RLS: every query here except GetFolderScope filters
-- by team_id, because Postgres enforces nothing about tenancy.
--
-- parent_folder_id is never written. Folders are flat in this iteration and the
-- column is left for a later plan; no statement below names it, so the column
-- default applies and it stays NULL.

-- GetFolderScope is the one deliberate exception to the team_id rule. It is
-- what the FolderEditorScope resolver calls to *discover* which team a folder
-- belongs to, so it cannot filter by the answer. Everything the handler does
-- afterwards is filtered by the team_id this returns.

-- name: GetFolderScope :one
select id, team_id
from folder
where id = $1;

-- GetFolderInTeam validates a folder_id supplied in a link's request body. The
-- team_id comes from the authorization scope, never from the request, so a
-- folder belonging to another team simply returns no row.

-- name: GetFolderInTeam :one
select id, name
from folder
where team_id = $1 and id = $2;

-- name: CreateFolder :one
insert into folder (team_id, name)
values ($1, $2)
returning id, team_id, name, created_at;

-- Paginated. count(*) over () gives the total in the same scan, so the list and
-- its total_count cannot disagree the way two separate queries can. Ordered by
-- name because a folder list is something a human reads alphabetically.

-- name: ListFoldersForTeam :many
select id, team_id, name, created_at, count(*) over () as total_count
from folder
where team_id = $1
order by name
limit $2 offset $3;

-- CountFoldersForTeam serves two callers: the pagination total when the page
-- is past the end and the window function returned no rows, and the per-team
-- cap check before an insert.

-- name: CountFoldersForTeam :one
select count(*) from folder where team_id = $1;

-- name: UpdateFolder :one
update folder
set name = $3
where id = $1 and team_id = $2
returning id, team_id, name, created_at;

-- CountLinksInFolder is read before a delete, for the audit metadata. It is a
-- separate statement rather than a CTE beside the delete so that the number
-- reported is unambiguously the pre-delete count.

-- name: CountLinksInFolder :one
select count(*) from link where folder_id = $1 and team_id = $2;

-- DeleteFolder returns the id so the handler can tell "deleted" from "no such
-- folder in this team" without a second round trip. Links in the folder are
-- unfiled by the on delete set null foreign key, not by application code.

-- name: DeleteFolder :one
delete from folder
where id = $1 and team_id = $2
returning id;
```

- [ ] **Step 2: Generate**

Run: `cd apps/api && sqlc generate` Expected: no error; `internal/db/` gains the generated methods.

- [ ] **Step 3: Verify the generated signatures**

Run: `cd apps/api && grep -n "func (q \*Queries) \(Get\|Create\|List\|Count\|Update\|Delete\)Folder\|CountLinksInFolder" internal/db/*.go` Expected: eight methods, one per statement above.

- [ ] **Step 4: Confirm the package still builds**

Run: `cd apps/api && go build ./...` Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/db
git commit -m "feat(db): add folder queries"
```

---

### Task 5: Folder-scoped authorization

**Files:**

- Create: `apps/api/internal/authz/folder.go`
- Create: `apps/api/internal/authz/folder_test.go`

**Interfaces:**

- Consumes: `db.Queries.GetFolderScope` (Task 4); `resolveMembership` (Task 3).
- Produces: `authz.FolderPath`, `authz.ResolvedFolder{ID, TeamID}`, `authz.FolderResolver`, `authz.WithFolderResolver`, `authz.NewQueryFolderResolver`, `authz.FolderEditorScope` with `Member()` and `Folder()`, and `authz.ErrFolderNotFound`. Tasks 6 and 13 use them.

**Read `apps/api/internal/authz/link.go` before writing this.** It is the same pattern and the 404-versus-403 asymmetry is easy to get subtly wrong. There is deliberately **no** `FolderViewerScope`: folders have no read-one endpoint, so a viewer scope would have no caller.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/authz/folder_test.go` as **`package authz_test`** — every test file in this package is external, and this one must be too. It drives the scope through `humatest` exactly as `link_test.go` drives `LinkEditorScope`: register a probe operation whose input embeds the scope, install the fakes as middleware, and assert on the recorded HTTP status. Do not call `resolveFolderScope` directly; an external test cannot see it, and the point is to exercise the scope the way Huma runs it.

`fakeMembershipResolver` already exists in `link_test.go` and is reused as-is. Add only `fakeFolderResolver`, the probe input, and `folderScopeCase` — modelled line-for-line on `linkScopeCase`.

```go
package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

type fakeFolderResolver struct {
	folder authz.ResolvedFolder
	err    error
}

func (f fakeFolderResolver) Folder(context.Context, uuid.UUID) (authz.ResolvedFolder, error) {
	return f.folder, f.err
}

type folderEditorInput struct {
	authz.FolderEditorScope
}

// folderScopeCase wires one request through a registered operation whose input
// embeds FolderEditorScope, so the scope is exercised exactly as Huma runs it.
func folderScopeCase(
	t *testing.T, folders authz.FolderResolver, members authz.Resolver,
	userID uuid.UUID, folderID string,
) *httptest.ResponseRecorder {
	t.Helper()

	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		inner := ctx.Context()
		if userID != uuid.Nil {
			inner = auth.WithClaims(inner, auth.Claims{UserID: userID})
		}
		if members != nil {
			inner = authz.WithResolver(inner, members)
		}
		if folders != nil {
			inner = authz.WithFolderResolver(inner, folders)
		}
		next(huma.WithContext(ctx, inner))
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/folders/{folder_id}",
	}, func(_ context.Context, _ *folderEditorInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api.Get("/folders/" + folderID)
}

func TestFolderScopeAllowsAnEditor(t *testing.T) {
	teamID, folderID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
		}},
		userID, folderID.String())

	require.Equal(t, http.StatusOK, resp.Code)
}

func TestFolderScopeRefusesAViewerWith403(t *testing.T) {
	// A member of the owning team whose role is too low gets 403, not 404:
	// that caller already knows the folder exists.
	teamID, folderID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}},
		userID, folderID.String())

	require.Equal(t, http.StatusForbidden, resp.Code)
}

func TestFolderScopeHidesAnotherTeamsFolderWith404(t *testing.T) {
	// A folder owned by another team and a folder that does not exist must be
	// indistinguishable, or folder IDs become probeable.
	folderID, userID := uuid.New(), uuid.New()

	foreign := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: uuid.New()}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		userID, folderID.String())

	missing := folderScopeCase(t,
		fakeFolderResolver{err: authz.ErrFolderNotFound},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		userID, folderID.String())

	require.Equal(t, http.StatusNotFound, foreign.Code)
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Equal(t, missing.Body.String(), foreign.Body.String(),
		"a foreign folder and a missing one must be byte-identical")
}

func TestFolderScopeReturns422ForAMalformedID(t *testing.T) {
	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), "not-a-uuid")

	require.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestFolderScopeRefusesAnUnauthenticatedCaller(t *testing.T) {
	folderID := uuid.New()

	resp := folderScopeCase(t,
		fakeFolderResolver{folder: authz.ResolvedFolder{ID: folderID, TeamID: uuid.New()}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.Nil, folderID.String())

	require.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestFolderScopeRefusesWhenNoResolverIsInstalled(t *testing.T) {
	folderID := uuid.New()

	resp := folderScopeCase(t, nil,
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), folderID.String())

	require.Equal(t, http.StatusInternalServerError, resp.Code)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/authz/ -run TestFolderEditorScope -v` Expected: FAIL — `undefined: FolderEditorScope`.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/authz/folder.go`:

```go
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// ErrFolderNotFound means no folder has that ID. It is answered with 404 — as
// is a folder belonging to a team the caller is not in, so the two are
// indistinguishable from outside and folder IDs cannot be probed.
var ErrFolderNotFound = errors.New("authz: folder does not exist")

// FolderPath carries the folder ID every folder-scoped operation takes in its
// path. Exported and embedded by value for the same reason TeamPath is:
// reflection cannot reliably set fields promoted through an unexported
// embedded struct.
type FolderPath struct {
	FolderID uuid.UUID `path:"folder_id" doc:"The folder this request operates on."`
}

// ResolvedFolder is what the scope loaded on the way to its decision.
type ResolvedFolder struct {
	ID     uuid.UUID
	TeamID uuid.UUID
}

// FolderResolver loads the tenancy facts about a folder. Implemented by
// QueryFolderResolver against Postgres, and by fakes in tests.
type FolderResolver interface {
	Folder(ctx context.Context, folderID uuid.UUID) (ResolvedFolder, error)
}

type folderResolverKey struct{}

// WithFolderResolver returns a context carrying the folder resolver. The /v1
// auth middleware installs it once per request, beside the others.
func WithFolderResolver(ctx context.Context, r FolderResolver) context.Context {
	return context.WithValue(ctx, folderResolverKey{}, r)
}

func folderResolverFromContext(ctx context.Context) (FolderResolver, bool) {
	r, ok := ctx.Value(folderResolverKey{}).(FolderResolver)
	return r, ok
}

// QueryFolderResolver is the production FolderResolver: one primary-key lookup
// per folder-scoped request.
type QueryFolderResolver struct {
	queries *db.Queries
}

// NewQueryFolderResolver builds a FolderResolver backed by queries.
func NewQueryFolderResolver(queries *db.Queries) QueryFolderResolver {
	return QueryFolderResolver{queries: queries}
}

// Folder implements FolderResolver.
func (r QueryFolderResolver) Folder(
	ctx context.Context, folderID uuid.UUID,
) (ResolvedFolder, error) {
	row, err := r.queries.GetFolderScope(ctx, folderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedFolder{}, ErrFolderNotFound
	}
	if err != nil {
		return ResolvedFolder{}, fmt.Errorf("authz: load folder scope: %w", err)
	}
	return ResolvedFolder{ID: row.ID, TeamID: row.TeamID}, nil
}

// FolderEditorScope is embedded by folder operations requiring at least
// editor. There is no FolderViewerScope: folders have no read-one endpoint —
// reads go through the team-scoped list, which uses ViewerScope — so a viewer
// scope here would be a resolver with no caller.
type FolderEditorScope struct {
	FolderPath
	member Membership
	folder ResolvedFolder
}

// Resolve loads the folder and checks the caller's membership before the
// handler runs.
func (s *FolderEditorScope) Resolve(ctx huma.Context) []error {
	return resolveFolderScope(ctx, s.FolderID, RoleEditor, &s.member, &s.folder)
}

// Member returns the membership Resolve loaded.
func (s *FolderEditorScope) Member() Membership { return s.member }

// Folder returns the folder Resolve loaded.
func (s *FolderEditorScope) Folder() ResolvedFolder { return s.folder }

// resolveFolderScope turns a folder ID into an authorization decision: who is
// calling, which team owns the folder, and whether that caller's role in that
// team is enough. The team is discovered here rather than taken from the path,
// which is the whole reason this scope exists.
func resolveFolderScope(
	ctx huma.Context, folderID uuid.UUID, required Role, member *Membership, out *ResolvedFolder,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed folder_id would otherwise be reported as a plain 404 — the
	// wrong defect. Same guard, same reason, as the one in resolveLinkScope.
	if raw := ctx.Param("folder_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("folder_id must be a valid UUID")}
		}
	}

	resolver, ok := folderResolverFromContext(ctx.Context())
	if !ok {
		// Refusing is the only safe answer: without a resolver there is no way
		// to know which team owns this folder.
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Folder(ctx.Context(), folderID)
	switch {
	case errors.Is(err, ErrFolderNotFound):
		return []error{huma.Error404NotFound("folder not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the folder")}
	}

	// A non-member gets the same 404 a missing folder gets. An insufficient
	// role gets 403: that caller already knows the folder exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "folder not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/authz/ -v` Expected: PASS, all four new tests and every existing one.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/authz/folder.go apps/api/internal/authz/folder_test.go
git commit -m "feat(api): add folder-scoped authorization"
```

---

### Task 6: Folder endpoints

**Files:**

- Modify: `apps/api/internal/audit/audit.go`
- Create: `apps/api/internal/api/folders.go`
- Create: `apps/api/internal/api/folders_test.go`
- Modify: `apps/api/internal/api/v1.go`
- Modify: `apps/api/internal/api/matrix_test.go`

**Interfaces:**

- Consumes: Task 2's `validateResourceName` and `maxFoldersPerTeam`; Task 4's queries; Task 5's `FolderEditorScope` and `NewQueryFolderResolver`.
- Produces: `api.Folder{ID, TeamID, Name, CreatedAt}`, the four operation IDs `create-folder`, `list-folders`, `update-folder`, `delete-folder`, and `audit.ActionFolderCreated/Updated/Deleted` plus `audit.EntityFolder`. Tasks 10–13 use `api.Folder` and the audit actions.

- [ ] **Step 1: Add the audit actions**

In `apps/api/internal/audit/audit.go`, extend the entity constants:

```go
	EntityTeam       = "team"
	EntityTeamMember = "team_member"
	EntityLink       = "link"
	EntityFolder     = "folder"
	EntityTag        = "tag"
)
```

and the action block, below the link actions:

```go
	// Folder and tag changes made through a link write do not get their own
	// action: they are part of that write's link.updated row, with the
	// affected fields in metadata.changed. The rule above — one row per PATCH,
	// not one per changed field — governs those too.
	ActionFolderCreated Action = "folder.created"
	ActionFolderUpdated Action = "folder.updated"
	ActionFolderDeleted Action = "folder.deleted"

	ActionTagCreated Action = "tag.created"
	ActionTagUpdated Action = "tag.updated"
	ActionTagDeleted Action = "tag.deleted"
)
```

Both tag actions are added here rather than in Task 9 so that `CheckAction`'s taxonomy is edited once.

- [ ] **Step 2: Write the failing tests**

Create `apps/api/internal/api/folders_test.go`. Use the same harness the existing `links_test.go` uses — copy its setup helper calls verbatim rather than inventing new ones:

```go
package api

import (
	"net/http"
	"testing"
)

func TestCreateFolderStoresTrimmedName(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/folders",
		map[string]any{"name": "  Sommerfest 2026  "})

	if resp.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.Code, resp.Body.String())
	}
	var body Folder
	h.decode(t, resp, &body)
	if body.Name != "Sommerfest 2026" {
		t.Fatalf("got %q, want the trimmed name", body.Name)
	}
	if body.TeamID != team.ID {
		t.Fatalf("folder was created for the wrong team")
	}
}

func TestCreateFolderLeavesParentFolderIDNull(t *testing.T) {
	// Folders are flat in this iteration. parent_folder_id stays in the schema
	// for a later plan, and the API must never write it — a property that
	// decays silently, so it is asserted rather than assumed.
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/folders",
		map[string]any{"name": "Presse"})
	if resp.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201", resp.Code)
	}

	var nulls int
	err := h.Pool.QueryRow(t.Context(),
		`select count(*) from folder where team_id = $1 and parent_folder_id is null`,
		team.ID).Scan(&nulls)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	var total int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from folder where team_id = $1`, team.ID).Scan(&total); err != nil {
		t.Fatalf("query: %v", err)
	}
	if nulls != total || total == 0 {
		t.Fatalf("got %d of %d folders with a NULL parent; the API must never set one", nulls, total)
	}
}

func TestCreateFolderRejectsEmptyName(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/folders",
		map[string]any{"name": "   "})

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", resp.Code)
	}
}

func TestCreateFolderEnforcesTheTeamCap(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	// Seed to the cap directly: going through the endpoint 100 times would
	// make this test the slowest in the suite for no extra coverage.
	if _, err := h.Pool.Exec(t.Context(),
		`insert into folder (team_id, name)
		 select $1, 'seed-' || g from generate_series(1, $2) g`,
		team.ID, maxFoldersPerTeam); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/folders",
		map[string]any{"name": "one too many"})

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422 at the cap: %s", resp.Code, resp.Body.String())
	}

	var count int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from folder where team_id = $1`, team.ID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != maxFoldersPerTeam {
		t.Fatalf("got %d folders, want the cap to have held at %d", count, maxFoldersPerTeam)
	}
}

func TestListFoldersOrdersByName(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	for _, name := range []string{"Zeltlager", "Ausflug", "Presse"} {
		if resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/folders",
			map[string]any{"name": name}); resp.Code != http.StatusCreated {
			t.Fatalf("seeding %q: got %d", name, resp.Code)
		}
	}

	resp := h.get(t, token, "/v1/teams/"+team.ID.String()+"/folders")
	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}
	var page Page[Folder]
	h.decode(t, resp, &page)

	want := []string{"Ausflug", "Presse", "Zeltlager"}
	if len(page.Items) != len(want) {
		t.Fatalf("got %d folders, want %d", len(page.Items), len(want))
	}
	for i, name := range want {
		if page.Items[i].Name != name {
			t.Fatalf("position %d: got %q, want %q", i, page.Items[i].Name, name)
		}
	}
}

func TestDeleteFolderUnfilesItsLinks(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	folder := h.createFolder(t, token, team.ID, "Sommerfest")
	link := h.createLinkInFolder(t, token, team.ID, "https://example.org/fest", folder.ID)

	resp := h.delete(t, token, "/v1/folders/"+folder.ID.String())
	if resp.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204: %s", resp.Code, resp.Body.String())
	}

	// The link survives, unfiled. Nothing is destroyed, which is why a
	// non-empty folder is not refused.
	var folderID *string
	err := h.Pool.QueryRow(t.Context(),
		`select folder_id::text from link where id = $1`, link.ID).Scan(&folderID)
	if err != nil {
		t.Fatalf("the link must still exist: %v", err)
	}
	if folderID != nil {
		t.Fatalf("got folder_id %v, want NULL", *folderID)
	}
}

func TestDeleteFolderRecordsHowManyLinksWereUnfiled(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	folder := h.createFolder(t, token, team.ID, "Sommerfest")
	h.createLinkInFolder(t, token, team.ID, "https://example.org/a", folder.ID)
	h.createLinkInFolder(t, token, team.ID, "https://example.org/b", folder.ID)

	if resp := h.delete(t, token, "/v1/folders/"+folder.ID.String()); resp.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", resp.Code)
	}

	var unfiled int
	err := h.Pool.QueryRow(t.Context(),
		`select (metadata->>'links_unfiled')::int from audit_log
		 where team_id = $1 and action = 'folder.deleted'`, team.ID).Scan(&unfiled)
	if err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if unfiled != 2 {
		t.Fatalf("got links_unfiled %d, want 2", unfiled)
	}
}
```

Add `createFolder` and `createLinkInFolder` to `testhelper_test.go` beside the existing helpers, following their signatures and error handling.

- [ ] **Step 3: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run TestCreateFolder -v` Expected: FAIL — `undefined: Folder`.

- [ ] **Step 4: Write the handlers**

Create `apps/api/internal/api/folders.go`:

```go
package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/audit"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// Folder is a folder as the API reports it. parent_folder_id is deliberately
// absent: the API never writes it, so publishing it would advertise a
// capability that does not exist.
type Folder struct {
	ID        uuid.UUID `json:"id"`
	TeamID    uuid.UUID `json:"team_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// CreateFolderInput declares its authorization in its type: EditorScope
// resolves and checks the caller's role before this handler's body runs.
type CreateFolderInput struct {
	authz.EditorScope
	Body struct {
		Name string `json:"name" maxLength:"60" doc:"Trimmed on input. Must not be empty."`
	}
}

// FolderOutput wraps a single folder resource.
type FolderOutput struct {
	Status int
	Body   Folder
}

// ListFoldersInput takes no filters: a list capped at 100 rows and ordered by
// name does not need them.
type ListFoldersInput struct {
	authz.ViewerScope
	PageParams
}

// ListFoldersOutput wraps a paginated list of folders.
type ListFoldersOutput struct {
	Body Page[Folder]
}

// UpdateFolderInput declares its authorization in its type: FolderEditorScope
// resolves which team owns the folder and requires at least the editor role.
type UpdateFolderInput struct {
	authz.FolderEditorScope
	Body struct {
		Name string `json:"name" maxLength:"60"`
	}
}

// DeleteFolderInput declares its authorization in its type.
type DeleteFolderInput struct {
	authz.FolderEditorScope
}

// DeleteFolderOutput carries no body: a successful delete is 204 No Content.
type DeleteFolderOutput struct {
	Status int
}

func (d Deps) registerFolders(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-folder",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/folders",
		Summary:       "Create a folder",
		Tags:          []string{"Folders"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createFolder)

	huma.Register(api, huma.Operation{
		OperationID: "list-folders",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/folders",
		Summary:     "List a team's folders",
		Tags:        []string{"Folders"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listFolders)

	huma.Register(api, huma.Operation{
		OperationID: "update-folder",
		Method:      http.MethodPatch,
		Path:        "/v1/folders/{folder_id}",
		Summary:     "Rename a folder",
		Tags:        []string{"Folders"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateFolder)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-folder",
		Method:        http.MethodDelete,
		Path:          "/v1/folders/{folder_id}",
		Summary:       "Delete a folder",
		Tags:          []string{"Folders"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.deleteFolder)
}

func (d Deps) createFolder(ctx context.Context, in *CreateFolderInput) (*FolderOutput, error) {
	member := in.Member()

	name, err := validateResourceName(in.Body.Name)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	var created db.CreateFolderRow
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// Check-then-act inside the transaction. Two concurrent creates can
		// still both pass and leave the team one row over the cap; that is
		// accepted, because the cap protects a 500 MB budget rather than an
		// invariant, and closing the race would need an advisory lock on every
		// create.
		count, err := q.CountFoldersForTeam(ctx, member.TeamID)
		if err != nil {
			return err
		}
		if count >= maxFoldersPerTeam {
			return errFolderCapReached
		}

		row, err := q.CreateFolder(ctx, db.CreateFolderParams{
			TeamID: member.TeamID,
			Name:   name,
		})
		if err != nil {
			return err
		}
		created = row

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionFolderCreated,
			EntityType:  audit.EntityFolder,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case errors.Is(err, errFolderCapReached):
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("a team may have at most %d folders", maxFoldersPerTeam))
	case err != nil:
		d.Log.Error("create folder", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not create the folder")
	}

	return &FolderOutput{Status: http.StatusCreated, Body: folderResponse(created)}, nil
}

// errFolderCapReached and errTagCapReached travel out of the transaction so
// the cap becomes a 422 rather than a 500. They never reach the client.
var (
	errFolderCapReached = errors.New("api: folder cap reached")
	errTagCapReached    = errors.New("api: tag cap reached")
)

func (d Deps) listFolders(ctx context.Context, in *ListFoldersInput) (*ListFoldersOutput, error) {
	member := in.Member()

	rows, err := d.Queries.ListFoldersForTeam(ctx, db.ListFoldersForTeamParams{
		TeamID: member.TeamID,
		Limit:  in.Limit(),
		Offset: in.Offset(),
	})
	if err != nil {
		d.Log.Error("list folders", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not list folders")
	}

	items := make([]Folder, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.TotalCount
		items = append(items, Folder{
			ID: row.ID, TeamID: row.TeamID, Name: row.Name, CreatedAt: row.CreatedAt,
		})
	}

	if NeedsTotalFallback(in.PageParams, len(rows)) {
		total, err = d.Queries.CountFoldersForTeam(ctx, member.TeamID)
		if err != nil {
			d.Log.Error("count folders", "error", err, "team_id", member.TeamID)
			return nil, huma.Error500InternalServerError("could not list folders")
		}
	}

	return &ListFoldersOutput{Body: NewPage(items, in.PageParams, total)}, nil
}

func (d Deps) updateFolder(ctx context.Context, in *UpdateFolderInput) (*FolderOutput, error) {
	member := in.Member()

	name, err := validateResourceName(in.Body.Name)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	var updated db.UpdateFolderRow
	err = db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// The scope already authorized this caller. The team filter is here
		// anyway: it is what a reviewer can see, and the matrix test cannot
		// see a missing one.
		row, err := q.UpdateFolder(ctx, db.UpdateFolderParams{
			ID:     in.Folder().ID,
			TeamID: member.TeamID,
			Name:   name,
		})
		if err != nil {
			return err
		}
		updated = row

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionFolderUpdated,
			EntityType:  audit.EntityFolder,
			EntityID:    row.ID,
			Metadata:    map[string]any{"name": row.Name},
		})
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("folder not found")
	case err != nil:
		d.Log.Error("update folder", "error", err, "folder_id", in.FolderID)
		return nil, huma.Error500InternalServerError("could not update the folder")
	}

	return &FolderOutput{Status: http.StatusOK, Body: folderResponse(db.CreateFolderRow(updated))}, nil
}

func (d Deps) deleteFolder(ctx context.Context, in *DeleteFolderInput) (*DeleteFolderOutput, error) {
	member := in.Member()

	// folder is bound to a local because &in.Folder().ID does not compile: the
	// method returns a value, and a value's field is not addressable.
	folder := in.Folder()

	err := db.InTx(ctx, d.Pool, func(q *db.Queries) error {
		// Counted before the delete, so the number is unambiguously the
		// pre-delete count. The links themselves are unfiled by the
		// on delete set null foreign key, not by application code.
		unfiled, err := q.CountLinksInFolder(ctx, db.CountLinksInFolderParams{
			FolderID: &folder.ID,
			TeamID:   member.TeamID,
		})
		if err != nil {
			return err
		}

		id, err := q.DeleteFolder(ctx, db.DeleteFolderParams{
			ID: folder.ID, TeamID: member.TeamID,
		})
		if err != nil {
			return err
		}

		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionFolderDeleted,
			EntityType:  audit.EntityFolder,
			EntityID:    id,
			Metadata:    map[string]any{"links_unfiled": unfiled},
		})
	})

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, huma.Error404NotFound("folder not found")
	case err != nil:
		d.Log.Error("delete folder", "error", err, "folder_id", in.FolderID)
		return nil, huma.Error500InternalServerError("could not delete the folder")
	}

	return &DeleteFolderOutput{Status: http.StatusNoContent}, nil
}

func folderResponse(row db.CreateFolderRow) Folder {
	return Folder{ID: row.ID, TeamID: row.TeamID, Name: row.Name, CreatedAt: row.CreatedAt}
}
```

Add `"fmt"` and `"time"` to the import block. If sqlc's generated `UpdateFolderRow` is not convertible to `CreateFolderRow`, give `folderResponse` two small siblings rather than forcing the conversion — the same reasoning `links.go` records for `linkRow`.

- [ ] **Step 5: Register the routes and the resolver**

In `apps/api/internal/api/v1.go`, call `d.registerFolders(api)` beside `d.registerLinks(api)`, and install the resolver where `authz.WithLinkResolver` is installed:

```go
	ctx = authz.WithFolderResolver(ctx, authz.NewQueryFolderResolver(d.Queries))
```

- [ ] **Step 6: Add the matrix rows**

In `apps/api/internal/api/matrix_test.go`, append to `teamScopedCases`:

```go
	{"create-folder", http.MethodPost, "/v1/teams/{team}/folders",
		map[string]string{"name": "Matrix"}, authz.RoleEditor},
	{"list-folders", http.MethodGet, "/v1/teams/{team}/folders", nil, authz.RoleViewer},
	{"update-folder", http.MethodPatch, "/v1/folders/{folder}",
		map[string]string{"name": "Matrix umbenannt"}, authz.RoleEditor},
	{"delete-folder", http.MethodDelete, "/v1/folders/{folder}", nil, authz.RoleEditor},
```

The `{folder}` placeholder needs the same substitution `{link}` gets. Extend the harness that resolves those placeholders so it seeds a folder per team, mirroring how it seeds a link.

- [ ] **Step 7: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -v` Expected: PASS, including `TestEveryOperationIsAccountedFor` with the four new operations.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal/api apps/api/internal/audit
git commit -m "feat(api): add folder endpoints"
```

---

### Task 7: Tag queries

**Files:**

- Create: `apps/api/internal/db/queries/tag.sql`

**Interfaces:**

- Consumes: Task 1's unique index.
- Produces: `GetTagScope`, `ListTagsByIDs`, `CreateTag`, `ListTagsForTeam`, `CountTagsForTeam`, `UpdateTag`, `DeleteTag`, `ListTagsForLinks`, `DeleteLinkTags`, `InsertLinkTags` on `*db.Queries`. Tasks 8, 9, 10, 11 and 12 call them.

- [ ] **Step 1: Write the queries**

Create `apps/api/internal/db/queries/tag.sql`:

```sql
-- Tag CRUD and the link_tag join. There is no RLS: every query here except
-- GetTagScope filters by team_id, because Postgres enforces nothing about
-- tenancy.

-- GetTagScope is the one deliberate exception to the team_id rule. It is what
-- the TagEditorScope resolver calls to *discover* which team a tag belongs to,
-- so it cannot filter by the answer.

-- name: GetTagScope :one
select id, team_id
from tag
where id = $1;

-- ListTagsByIDs validates the tag_ids supplied in a link's request body. The
-- team_id comes from the authorization scope, never from the request, so a tag
-- belonging to another team simply does not come back — and the handler names
-- the missing id in a 422. The message is identical for a nonexistent id and
-- another team's id, so nothing leaks.

-- name: ListTagsByIDs :many
select id, name
from tag
where team_id = $1 and id = any(@ids::uuid[]);

-- name: CreateTag :one
insert into tag (team_id, name)
values ($1, $2)
returning id, team_id, name;

-- name: ListTagsForTeam :many
select id, team_id, name, count(*) over () as total_count
from tag
where team_id = $1
order by name
limit $2 offset $3;

-- name: CountTagsForTeam :one
select count(*) from tag where team_id = $1;

-- name: UpdateTag :one
update tag
set name = $3
where id = $1 and team_id = $2
returning id, team_id, name;

-- DeleteTag returns the name as well as the id: the audit row records which
-- tag was deleted, and returning it here costs nothing over a second lookup.

-- name: DeleteTag :one
delete from tag
where id = $1 and team_id = $2
returning id, name;

-- ListTagsForLinks is the second query the link list runs, once per page. A
-- left join onto the list query itself would be wrong rather than merely
-- slower: it multiplies rows before LIMIT applies, so a page of 20 links would
-- silently return fewer. The tag.team_id filter is redundant given the link
-- ids are already the caller's, and it stays because a query file is read on
-- its own.

-- name: ListTagsForLinks :many
select lt.link_id, t.id, t.name
from link_tag lt
join tag t on t.id = lt.tag_id
where lt.link_id = any(@link_ids::uuid[]) and t.team_id = $1
order by t.name;

-- DeleteLinkTags and InsertLinkTags implement whole-set replacement inside the
-- link write's own transaction. The delete is unconditional rather than a
-- computed diff: the set is at most ten rows, and a diff would be more code
-- for no measurable gain.

-- name: DeleteLinkTags :exec
delete from link_tag where link_id = $1;

-- name: InsertLinkTags :exec
insert into link_tag (link_id, tag_id)
select $1, unnest(@tag_ids::uuid[]);
```

- [ ] **Step 2: Generate**

Run: `cd apps/api && sqlc generate` Expected: no error.

- [ ] **Step 3: Verify the signatures**

Run: `cd apps/api && grep -n "func (q \*Queries) .*Tag" internal/db/*.go` Expected: ten methods.

- [ ] **Step 4: Build**

Run: `cd apps/api && go build ./...` Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/db
git commit -m "feat(db): add tag and link_tag queries"
```

---

### Task 8: Tag-scoped authorization

**Files:**

- Create: `apps/api/internal/authz/tag.go`
- Create: `apps/api/internal/authz/tag_test.go`

**Interfaces:**

- Consumes: `db.Queries.GetTagScope` (Task 7); `resolveMembership` (Task 3).
- Produces: `authz.TagPath`, `authz.ResolvedTag{ID, TeamID}`, `authz.TagResolver`, `authz.WithTagResolver`, `authz.NewQueryTagResolver`, `authz.TagEditorScope` with `Member()` and `Tag()`, and `authz.ErrTagNotFound`. Task 9 uses them.

This is Task 5 with `folder` replaced by `tag` throughout. Write it out rather than abstracting the two into one generic scope: the pair reads clearly, and a generic version would have to be parameterized over the resolver, the error, the path parameter name and the not-found message, which is more machinery than the duplication costs.

- [ ] **Step 1: Write the failing test**

Create `apps/api/internal/authz/tag_test.go` as **`package authz_test`**, structured exactly like `folder_test.go` from Task 5: a `fakeTagResolver`, a `tagEditorInput` probe struct, a `tagScopeCase` helper modelled on `folderScopeCase`, and the same six cases. `fakeMembershipResolver` is reused from `link_test.go`.

```go
package authz_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

type fakeTagResolver struct {
	tag authz.ResolvedTag
	err error
}

func (f fakeTagResolver) Tag(context.Context, uuid.UUID) (authz.ResolvedTag, error) {
	return f.tag, f.err
}

type tagEditorInput struct {
	authz.TagEditorScope
}

// tagScopeCase wires one request through a registered operation whose input
// embeds TagEditorScope, so the scope is exercised exactly as Huma runs it.
func tagScopeCase(
	t *testing.T, tags authz.TagResolver, members authz.Resolver,
	userID uuid.UUID, tagID string,
) *httptest.ResponseRecorder {
	t.Helper()

	_, api := humatest.New(t, huma.DefaultConfig("test", "1.0.0"))
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		inner := ctx.Context()
		if userID != uuid.Nil {
			inner = auth.WithClaims(inner, auth.Claims{UserID: userID})
		}
		if members != nil {
			inner = authz.WithResolver(inner, members)
		}
		if tags != nil {
			inner = authz.WithTagResolver(inner, tags)
		}
		next(huma.WithContext(ctx, inner))
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/tags/{tag_id}",
	}, func(_ context.Context, _ *tagEditorInput) (*struct{}, error) {
		return &struct{}{}, nil
	})

	return api.Get("/tags/" + tagID)
}

func TestTagScopeAllowsAnEditor(t *testing.T) {
	teamID, tagID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleEditor,
		}},
		userID, tagID.String())

	require.Equal(t, http.StatusOK, resp.Code)
}

func TestTagScopeRefusesAViewerWith403(t *testing.T) {
	teamID, tagID, userID := uuid.New(), uuid.New(), uuid.New()

	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: teamID}},
		fakeMembershipResolver{membership: authz.Membership{
			TeamID: teamID, UserID: userID, Role: authz.RoleViewer,
		}},
		userID, tagID.String())

	require.Equal(t, http.StatusForbidden, resp.Code)
}

func TestTagScopeHidesAnotherTeamsTagWith404(t *testing.T) {
	tagID, userID := uuid.New(), uuid.New()

	foreign := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: uuid.New()}},
		fakeMembershipResolver{err: authz.ErrNotMember},
		userID, tagID.String())

	missing := tagScopeCase(t,
		fakeTagResolver{err: authz.ErrTagNotFound},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		userID, tagID.String())

	require.Equal(t, http.StatusNotFound, foreign.Code)
	require.Equal(t, http.StatusNotFound, missing.Code)
	require.Equal(t, missing.Body.String(), foreign.Body.String(),
		"a foreign tag and a missing one must be byte-identical")
}

func TestTagScopeReturns422ForAMalformedID(t *testing.T) {
	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), "not-a-uuid")

	require.Equal(t, http.StatusUnprocessableEntity, resp.Code)
}

func TestTagScopeRefusesAnUnauthenticatedCaller(t *testing.T) {
	tagID := uuid.New()

	resp := tagScopeCase(t,
		fakeTagResolver{tag: authz.ResolvedTag{ID: tagID, TeamID: uuid.New()}},
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.Nil, tagID.String())

	require.Equal(t, http.StatusUnauthorized, resp.Code)
}

func TestTagScopeRefusesWhenNoResolverIsInstalled(t *testing.T) {
	resp := tagScopeCase(t, nil,
		fakeMembershipResolver{membership: authz.Membership{Role: authz.RoleOwner}},
		uuid.New(), uuid.New().String())

	require.Equal(t, http.StatusInternalServerError, resp.Code)
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `cd apps/api && go test ./internal/authz/ -run TestTagEditorScope -v` Expected: FAIL — `undefined: TagEditorScope`.

- [ ] **Step 3: Write the implementation**

Create `apps/api/internal/authz/tag.go` as the exact analogue of `folder.go`:

```go
package authz

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/db"
)

// ErrTagNotFound means no tag has that ID. It is answered with 404 — as is a
// tag belonging to a team the caller is not in, so the two are
// indistinguishable from outside and tag IDs cannot be probed.
var ErrTagNotFound = errors.New("authz: tag does not exist")

// TagPath carries the tag ID every tag-scoped operation takes in its path.
type TagPath struct {
	TagID uuid.UUID `path:"tag_id" doc:"The tag this request operates on."`
}

// ResolvedTag is what the scope loaded on the way to its decision.
type ResolvedTag struct {
	ID     uuid.UUID
	TeamID uuid.UUID
}

// TagResolver loads the tenancy facts about a tag.
type TagResolver interface {
	Tag(ctx context.Context, tagID uuid.UUID) (ResolvedTag, error)
}

type tagResolverKey struct{}

// WithTagResolver returns a context carrying the tag resolver.
func WithTagResolver(ctx context.Context, r TagResolver) context.Context {
	return context.WithValue(ctx, tagResolverKey{}, r)
}

func tagResolverFromContext(ctx context.Context) (TagResolver, bool) {
	r, ok := ctx.Value(tagResolverKey{}).(TagResolver)
	return r, ok
}

// QueryTagResolver is the production TagResolver: one primary-key lookup per
// tag-scoped request.
type QueryTagResolver struct {
	queries *db.Queries
}

// NewQueryTagResolver builds a TagResolver backed by queries.
func NewQueryTagResolver(queries *db.Queries) QueryTagResolver {
	return QueryTagResolver{queries: queries}
}

// Tag implements TagResolver.
func (r QueryTagResolver) Tag(ctx context.Context, tagID uuid.UUID) (ResolvedTag, error) {
	row, err := r.queries.GetTagScope(ctx, tagID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ResolvedTag{}, ErrTagNotFound
	}
	if err != nil {
		return ResolvedTag{}, fmt.Errorf("authz: load tag scope: %w", err)
	}
	return ResolvedTag{ID: row.ID, TeamID: row.TeamID}, nil
}

// TagEditorScope is embedded by tag operations requiring at least editor.
// There is no TagViewerScope, for the same reason there is no
// FolderViewerScope: tags have no read-one endpoint.
type TagEditorScope struct {
	TagPath
	member Membership
	tag    ResolvedTag
}

// Resolve loads the tag and checks the caller's membership before the handler runs.
func (s *TagEditorScope) Resolve(ctx huma.Context) []error {
	return resolveTagScope(ctx, s.TagID, RoleEditor, &s.member, &s.tag)
}

// Member returns the membership Resolve loaded.
func (s *TagEditorScope) Member() Membership { return s.member }

// Tag returns the tag Resolve loaded.
func (s *TagEditorScope) Tag() ResolvedTag { return s.tag }

// resolveTagScope turns a tag ID into an authorization decision: who is
// calling, which team owns the tag, and whether that caller's role in that
// team is enough.
func resolveTagScope(
	ctx huma.Context, tagID uuid.UUID, required Role, member *Membership, out *ResolvedTag,
) []error {
	if _, ok := auth.ClaimsFromContext(ctx.Context()); !ok {
		return []error{huma.Error401Unauthorized("not authenticated")}
	}

	// Huma runs every resolver even when its own parameter binding already
	// failed, and picks the last error's status when several are present. A
	// malformed tag_id would otherwise be reported as a plain 404.
	if raw := ctx.Param("tag_id"); raw != "" {
		if _, err := uuid.Parse(raw); err != nil {
			return []error{huma.Error422UnprocessableEntity("tag_id must be a valid UUID")}
		}
	}

	resolver, ok := tagResolverFromContext(ctx.Context())
	if !ok {
		return []error{huma.Error500InternalServerError("authorization is not configured")}
	}

	resolved, err := resolver.Tag(ctx.Context(), tagID)
	switch {
	case errors.Is(err, ErrTagNotFound):
		return []error{huma.Error404NotFound("tag not found")}
	case err != nil:
		return []error{huma.Error500InternalServerError("could not resolve the tag")}
	}

	// A non-member gets the same 404 a missing tag gets. An insufficient role
	// gets 403: that caller already knows the tag exists.
	if errs := resolveMembership(ctx, resolved.TeamID, required, "tag not found", member); len(errs) > 0 {
		return errs
	}

	*out = resolved
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `cd apps/api && go test ./internal/authz/ -v` Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add apps/api/internal/authz/tag.go apps/api/internal/authz/tag_test.go
git commit -m "feat(api): add tag-scoped authorization"
```

---

### Task 9: Tag endpoints

**Files:**

- Create: `apps/api/internal/api/tags.go`
- Create: `apps/api/internal/api/tags_test.go`
- Modify: `apps/api/internal/api/v1.go`
- Modify: `apps/api/internal/api/matrix_test.go`

**Interfaces:**

- Consumes: Task 2's `validateResourceName` and `maxTagsPerTeam`; Task 7's queries; Task 8's `TagEditorScope`; Task 6's audit actions.
- Produces: `api.Tag{ID, TeamID, Name}` and the operation IDs `create-tag`, `list-tags`, `update-tag`, `delete-tag`. Task 12 embeds `api.Tag` in the link representation.

- [ ] **Step 1: Write the failing tests**

Create `apps/api/internal/api/tags_test.go`:

```go
package api

import (
	"net/http"
	"testing"
)

func TestCreateTagKeepsTheTypedCase(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/tags",
		map[string]any{"name": "Sommerfest"})

	if resp.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.Code, resp.Body.String())
	}
	var body Tag
	h.decode(t, resp, &body)
	if body.Name != "Sommerfest" {
		t.Fatalf("got %q, want the capital kept", body.Name)
	}
}

func TestCreateTagRejectsACaseInsensitiveDuplicate(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	first := h.post(t, token, "/v1/teams/"+team.ID.String()+"/tags",
		map[string]any{"name": "Sommerfest"})
	if first.Code != http.StatusCreated {
		t.Fatalf("first create: got %d, want 201", first.Code)
	}

	second := h.post(t, token, "/v1/teams/"+team.ID.String()+"/tags",
		map[string]any{"name": "SOMMERFEST"})
	if second.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409: %s", second.Code, second.Body.String())
	}

	var count int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from tag where team_id = $1`, team.ID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d tags, want 1", count)
	}
}

func TestTwoTeamsMayEachHaveTheSameTagName(t *testing.T) {
	// Uniqueness is per team. Without the team_id half of the index this would
	// be a 409 and one Verein could block a name for every other.
	h := newTestHarness(t)
	one := h.newTeam(t, "Verein Eins")
	two := h.newTeam(t, "Verein Zwei")

	for _, team := range []testTeam{one, two} {
		token := h.tokenFor(t, team.OwnerID)
		resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/tags",
			map[string]any{"name": "Presse"})
		if resp.Code != http.StatusCreated {
			t.Fatalf("team %s: got %d, want 201", team.ID, resp.Code)
		}
	}
}

func TestUpdateTagRejectsACaseInsensitiveCollision(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	h.createTag(t, token, team.ID, "Presse")
	other := h.createTag(t, token, team.ID, "Sommerfest")

	resp := h.patch(t, token, "/v1/tags/"+other.ID.String(),
		map[string]any{"name": "presse"})

	if resp.Code != http.StatusConflict {
		t.Fatalf("got %d, want 409: %s", resp.Code, resp.Body.String())
	}
}

func TestCreateTagEnforcesTheTeamCap(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	if _, err := h.Pool.Exec(t.Context(),
		`insert into tag (team_id, name)
		 select $1, 'seed-' || g from generate_series(1, $2) g`,
		team.ID, maxTagsPerTeam); err != nil {
		t.Fatalf("seed: %v", err)
	}

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/tags",
		map[string]any{"name": "one too many"})

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422 at the cap", resp.Code)
	}
}

func TestDeleteTagDetachesItWithoutTouchingTheLinks(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	tag := h.createTag(t, token, team.ID, "Presse")
	link := h.createLinkWithTags(t, token, team.ID, "https://example.org/pm", tag.ID)

	if resp := h.delete(t, token, "/v1/tags/"+tag.ID.String()); resp.Code != http.StatusNoContent {
		t.Fatalf("got %d, want 204", resp.Code)
	}

	var joins int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from link_tag where link_id = $1`, link.ID).Scan(&joins); err != nil {
		t.Fatalf("query: %v", err)
	}
	if joins != 0 {
		t.Fatalf("got %d link_tag rows, want 0", joins)
	}

	var exists bool
	if err := h.Pool.QueryRow(t.Context(),
		`select exists(select 1 from link where id = $1)`, link.ID).Scan(&exists); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatalf("deleting a tag must not delete its links")
	}
}
```

Add `createTag` and `createLinkWithTags` to `testhelper_test.go`.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run TestCreateTag -v` Expected: FAIL — `undefined: Tag`.

- [ ] **Step 3: Write the handlers**

Create `apps/api/internal/api/tags.go`, following `folders.go` exactly. The differences from the folder handlers are three:

```go
// Tag is a tag as the API reports it. Names are unique per team
// case-insensitively but are stored and returned exactly as typed.
type Tag struct {
	ID     uuid.UUID `json:"id"`
	TeamID uuid.UUID `json:"team_id"`
	Name   string    `json:"name"`
}
```

First, a unique violation is a 409 on both create and update:

```go
	switch {
	case errors.Is(err, errTagCapReached):
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("a team may have at most %d tags", maxTagsPerTeam))
	case isUniqueViolation(err):
		// The index folds case, so this fires for "SOMMERFEST" against an
		// existing "Sommerfest" as well as for an exact repeat.
		return nil, huma.Error409Conflict("a tag with that name already exists")
	case err != nil:
		d.Log.Error("create tag", "error", err, "team_id", member.TeamID)
		return nil, huma.Error500InternalServerError("could not create the tag")
	}
```

`isUniqueViolation` already exists in `links.go`; reuse it rather than writing a second one.

Second, `Tag` has no `created_at`, because the schema has no such column — do not add one.

Third, the delete has no unfiled count to record:

```go
		return audit.Log(ctx, q, audit.Entry{
			TeamID:      member.TeamID,
			ActorUserID: member.UserID,
			Action:      audit.ActionTagDeleted,
			EntityType:  audit.EntityTag,
			EntityID:    id,
			Metadata:    map[string]any{"name": name},
		})
```

Everything else — the cap check inside the transaction, the `validateResourceName` call, the `team_id` filter on update and delete, the `pgx.ErrNoRows` to 404 mapping — mirrors `folders.go` with `folder` replaced by `tag`. These are the exact declarations, so nothing has to be inferred from the other file:

```go
type CreateTagInput struct {
	authz.EditorScope
	Body struct {
		Name string `json:"name" maxLength:"60" doc:"Trimmed on input. Unique per team, case-insensitively."`
	}
}

type TagOutput struct {
	Status int
	Body   Tag
}

type ListTagsInput struct {
	authz.ViewerScope
	PageParams
}

type ListTagsOutput struct {
	Body Page[Tag]
}

type UpdateTagInput struct {
	authz.TagEditorScope
	Body struct {
		Name string `json:"name" maxLength:"60"`
	}
}

type DeleteTagInput struct {
	authz.TagEditorScope
}

type DeleteTagOutput struct {
	Status int
}

func (d Deps) registerTags(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-tag",
		Method:        http.MethodPost,
		Path:          "/v1/teams/{team_id}/tags",
		Summary:       "Create a tag",
		Tags:          []string{"Tags"},
		DefaultStatus: http.StatusCreated,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.createTag)

	huma.Register(api, huma.Operation{
		OperationID: "list-tags",
		Method:      http.MethodGet,
		Path:        "/v1/teams/{team_id}/tags",
		Summary:     "List a team's tags",
		Tags:        []string{"Tags"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.listTags)

	huma.Register(api, huma.Operation{
		OperationID: "update-tag",
		Method:      http.MethodPatch,
		Path:        "/v1/tags/{tag_id}",
		Summary:     "Rename a tag",
		Tags:        []string{"Tags"},
		Security:    []map[string][]string{{"bearerAuth": {}}},
	}, d.updateTag)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-tag",
		Method:        http.MethodDelete,
		Path:          "/v1/tags/{tag_id}",
		Summary:       "Delete a tag",
		Tags:          []string{"Tags"},
		DefaultStatus: http.StatusNoContent,
		Security:      []map[string][]string{{"bearerAuth": {}}},
	}, d.deleteTag)
}

func tagResponse(row db.CreateTagRow) Tag {
	return Tag{ID: row.ID, TeamID: row.TeamID, Name: row.Name}
}
```

The four handler signatures are `createTag(ctx context.Context, in *CreateTagInput) (*TagOutput, error)`, `listTags(ctx context.Context, in *ListTagsInput) (*ListTagsOutput, error)`, `updateTag(ctx context.Context, in *UpdateTagInput) (*TagOutput, error)` and `deleteTag(ctx context.Context, in *DeleteTagInput) (*DeleteTagOutput, error)`.

`deleteTag` needs the tag's name for its audit metadata, which is why Task 7's `DeleteTag` returns `id, name` rather than the id alone — one statement, one round trip, no second lookup. Bind `tag := in.Tag()` before the transaction and use `tag.ID`: `&in.Tag().ID` does not compile, because a method's return value is not addressable.

- [ ] **Step 4: Register the routes and the resolver**

In `v1.go`, add `d.registerTags(api)` and:

```go
	ctx = authz.WithTagResolver(ctx, authz.NewQueryTagResolver(d.Queries))
```

- [ ] **Step 5: Add the matrix rows**

```go
	{"create-tag", http.MethodPost, "/v1/teams/{team}/tags",
		map[string]string{"name": "Matrix"}, authz.RoleEditor},
	{"list-tags", http.MethodGet, "/v1/teams/{team}/tags", nil, authz.RoleViewer},
	{"update-tag", http.MethodPatch, "/v1/tags/{tag}",
		map[string]string{"name": "Matrix umbenannt"}, authz.RoleEditor},
	{"delete-tag", http.MethodDelete, "/v1/tags/{tag}", nil, authz.RoleEditor},
```

Extend the placeholder substitution for `{tag}` as you did for `{folder}`. The matrix seeds one tag per team; because the create case posts the same name for every role under test, seed the matrix tag with a name the cases do not reuse, or the 409 will be mistaken for an authorization failure.

- [ ] **Step 6: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -v` Expected: PASS, with `TestEveryOperationIsAccountedFor` covering all eight new operations.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal/api
git commit -m "feat(api): add tag endpoints"
```

---

### Task 10: Folder and tag references on link create

**Files:**

- Modify: `apps/api/internal/db/queries/link_crud.sql`
- Modify: `apps/api/internal/api/links.go`
- Modify: `apps/api/internal/api/links_test.go`

**Interfaces:**

- Consumes: Task 4's `GetFolderInTeam`, Task 7's `ListTagsByIDs` and `InsertLinkTags`, Task 2's `maxTagsPerLink`.
- Produces: `Deps.resolveFolderRef(ctx, q, teamID, *uuid.UUID) (*uuid.UUID, error)` and `Deps.resolveTagRefs(ctx, q, teamID, []uuid.UUID) ([]Tag, error)`, both returning a Huma 422 for a bad reference. Task 11 calls both.

- [ ] **Step 1: Add folder_id to the link write and read queries**

In `apps/api/internal/db/queries/link_crud.sql`, add `folder_id` to `CreateLink`'s column list, values and returned projection, and to the projections of `GetLinkForAPI`, `ListLinksForTeam` and `UpdateLink`. For `CreateLink`:

```sql
-- name: CreateLink :one
with inserted as (
  insert into link (domain_id, team_id, slug, destination_url, redirect_type,
                    expires_at, analytics_enabled, created_by, folder_id)
  values ($1, $2, $3, $4, $5, $6, $7, $8, $9)
  returning *
)
select i.id, i.domain_id, i.team_id, d.hostname, i.slug, i.destination_url,
       i.redirect_type, i.state, i.expires_at,
       (i.password_hash is not null)::boolean as has_password,
       i.analytics_enabled, i.folder_id, i.created_by, i.created_at, i.updated_at
from inserted i
join domain d on d.id = i.domain_id;
```

Run `sqlc generate` and confirm `db.CreateLinkParams` gained `FolderID *uuid.UUID`.

- [ ] **Step 2: Write the failing tests**

Append to `apps/api/internal/api/links_test.go`:

```go
func TestCreateLinkFilesItInAFolder(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	folder := h.createFolder(t, token, team.ID, "Sommerfest")

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/fest",
		"folder_id":       folder.ID.String(),
	})

	if resp.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.Code, resp.Body.String())
	}
	var body Link
	h.decode(t, resp, &body)
	if body.FolderID == nil || *body.FolderID != folder.ID {
		t.Fatalf("got folder_id %v, want %s", body.FolderID, folder.ID)
	}
}

func TestCreateLinkRejectsAnotherTeamsFolder(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	token := h.tokenFor(t, mine.OwnerID)
	foreign := h.createFolder(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.post(t, token, "/v1/teams/"+mine.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/x",
		"folder_id":       foreign.ID.String(),
	})

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", resp.Code, resp.Body.String())
	}

	// And nothing was written.
	var count int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from link where team_id = $1`, mine.ID).Scan(&count); err != nil {
		t.Fatalf("query: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d links, want none created", count)
	}
}

func TestAnotherTeamsFolderIsIndistinguishableFromAMissingOne(t *testing.T) {
	// The two must be byte-identical, or folder IDs become probeable through
	// the link endpoint after being hidden on the folder endpoint.
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	token := h.tokenFor(t, mine.OwnerID)
	foreign := h.createFolder(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	foreignResp := h.post(t, token, "/v1/teams/"+mine.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/x",
		"folder_id":       foreign.ID.String(),
	})
	missingResp := h.post(t, token, "/v1/teams/"+mine.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/x",
		"folder_id":       uuid.New().String(),
	})

	if foreignResp.Code != missingResp.Code {
		t.Fatalf("status differs: %d vs %d", foreignResp.Code, missingResp.Code)
	}
	if foreignResp.Body.String() != missingResp.Body.String() {
		t.Fatalf("body differs:\n foreign: %s\n missing: %s",
			foreignResp.Body.String(), missingResp.Body.String())
	}
}

func TestCreateLinkAttachesTags(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	presse := h.createTag(t, token, team.ID, "Presse")
	fest := h.createTag(t, token, team.ID, "Sommerfest")

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/pm",
		"tag_ids":         []string{presse.ID.String(), fest.ID.String()},
	})

	if resp.Code != http.StatusCreated {
		t.Fatalf("got %d, want 201: %s", resp.Code, resp.Body.String())
	}
	var body Link
	h.decode(t, resp, &body)
	if len(body.Tags) != 2 {
		t.Fatalf("got %d tags, want 2", len(body.Tags))
	}
	// Tags come back ordered by name, so the response is stable.
	if body.Tags[0].Name != "Presse" || body.Tags[1].Name != "Sommerfest" {
		t.Fatalf("got %v, want Presse then Sommerfest", body.Tags)
	}
}

func TestCreateLinkRejectsAnotherTeamsTag(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	token := h.tokenFor(t, mine.OwnerID)
	foreign := h.createTag(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.post(t, token, "/v1/teams/"+mine.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/x",
		"tag_ids":         []string{foreign.ID.String()},
	})

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422: %s", resp.Code, resp.Body.String())
	}

	var joins int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from link_tag`).Scan(&joins); err != nil {
		t.Fatalf("query: %v", err)
	}
	if joins != 0 {
		t.Fatalf("got %d link_tag rows, want none", joins)
	}
}

func TestCreateLinkEnforcesTheTagsPerLinkCap(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)

	ids := make([]string, 0, maxTagsPerLink+1)
	for i := range maxTagsPerLink + 1 {
		tag := h.createTag(t, token, team.ID, fmt.Sprintf("tag-%02d", i))
		ids = append(ids, tag.ID.String())
	}

	resp := h.post(t, token, "/v1/teams/"+team.ID.String()+"/links", map[string]any{
		"destination_url": "https://example.org/x",
		"tag_ids":         ids,
	})

	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d, want 422", resp.Code)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'TestCreateLink(FilesIt|Rejects|Attaches|Enforces)' -v` Expected: FAIL — the body fields do not exist, so Huma rejects the unknown fields with 422 for the wrong reason. Read the failure output and confirm the message is about an unknown field, not about a reference: a test that passes for the wrong reason is worse than one that fails.

- [ ] **Step 4: Add the reference resolvers**

In `apps/api/internal/api/links.go`:

```go
// resolveFolderRef validates a folder_id from a request body against the
// caller's team. The team comes from the authorization scope, never from the
// request, so a folder belonging to another team simply returns no row.
//
// The 422 names the id and is byte-identical whether the folder does not exist
// or belongs to someone else — an attacker learns only that an id they guessed
// is not theirs, which they knew.
func (d Deps) resolveFolderRef(
	ctx context.Context, q *db.Queries, teamID uuid.UUID, folderID *uuid.UUID,
) (*uuid.UUID, error) {
	if folderID == nil {
		return nil, nil
	}

	row, err := q.GetFolderInTeam(ctx, db.GetFolderInTeamParams{
		TeamID: teamID, ID: *folderID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("no folder %s in this team", folderID))
	}
	if err != nil {
		return nil, fmt.Errorf("resolve folder reference: %w", err)
	}

	return &row.ID, nil
}

// resolveTagRefs validates tag_ids from a request body against the caller's
// team and returns the tags in the order the response will carry them. A
// missing id — nonexistent, or another team's — is a 422 naming it.
func (d Deps) resolveTagRefs(
	ctx context.Context, q *db.Queries, teamID uuid.UUID, tagIDs []uuid.UUID,
) ([]Tag, error) {
	if len(tagIDs) == 0 {
		return nil, nil
	}
	if len(tagIDs) > maxTagsPerLink {
		return nil, huma.Error422UnprocessableEntity(
			fmt.Sprintf("a link may have at most %d tags", maxTagsPerLink))
	}

	rows, err := q.ListTagsByIDs(ctx, db.ListTagsByIDsParams{TeamID: teamID, Ids: tagIDs})
	if err != nil {
		return nil, fmt.Errorf("resolve tag references: %w", err)
	}

	found := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		found[row.ID] = row.Name
	}
	for _, id := range tagIDs {
		if _, ok := found[id]; !ok {
			return nil, huma.Error422UnprocessableEntity(
				fmt.Sprintf("no tag %s in this team", id))
		}
	}

	// Sorted by name so the response order is stable regardless of the order
	// the caller sent, matching how ListTagsForLinks orders them on read.
	tags := make([]Tag, 0, len(rows))
	for _, row := range rows {
		tags = append(tags, Tag{ID: row.ID, TeamID: teamID, Name: row.Name})
	}
	slices.SortFunc(tags, func(a, b Tag) int { return strings.Compare(a.Name, b.Name) })
	return tags, nil
}
```

Add `"slices"` and `"strings"` to the imports.

- [ ] **Step 5: Wire them into createLink**

Extend `CreateLinkInput.Body`:

```go
		FolderID *uuid.UUID  `json:"folder_id,omitempty" doc:"Optional. Must be a folder in this team."`
		TagIDs   []uuid.UUID `json:"tag_ids,omitempty" maxItems:"10" doc:"Optional. Tags must belong to this team."`
```

Inside the existing `db.InTx` closure in `createLink`, before the `q.CreateLink` call:

```go
			folderID, err := d.resolveFolderRef(ctx, q, member.TeamID, in.Body.FolderID)
			if err != nil {
				return err
			}
			tags, err := d.resolveTagRefs(ctx, q, member.TeamID, in.Body.TagIDs)
			if err != nil {
				return err
			}
```

Extend the representation now rather than in Task 12, so this task's tests can pass on their own:

```go
type Link struct {
	// … every existing field, unchanged …
	FolderID *uuid.UUID `json:"folder_id"`
	Tags     []Tag      `json:"tags"`
}
```

Add `FolderID *uuid.UUID` to `linkRow` too, and set it in each `rowFrom*` converter. Task 12 fills `Tags` on the read paths; this task fills it on create, from the tags it has already resolved.

Pass `FolderID: folderID` in `db.CreateLinkParams`. After the insert, still inside the transaction:

```go
			if len(tags) > 0 {
				ids := make([]uuid.UUID, 0, len(tags))
				for _, tag := range tags {
					ids = append(ids, tag.ID)
				}
				if err := q.InsertLinkTags(ctx, db.InsertLinkTagsParams{
					LinkID: row.ID, TagIds: ids,
				}); err != nil {
					return err
				}
			}
			createdTags = tags
```

Declare `createdTags []Tag` beside `created` above the retry loop, and reset it at the top of each attempt so a slug collision does not carry tags from the abandoned attempt.

Then set `Tags` on the returned body, since this path already knows the answer and must not re-query for it:

```go
			body := d.linkResponse(rowFromCreate(created))
			body.Tags = createdTags
			if body.Tags == nil {
				// Never null in JSON: a client iterating tags should not have
				// to nil-check.
				body.Tags = []Tag{}
			}
			return &LinkOutput{Status: http.StatusCreated, Body: body}, nil
```

Because a Huma error returned from inside the transaction closure aborts it, add the Huma error to the switch that already classifies the transaction's error:

```go
		var status huma.StatusError
		if errors.As(err, &status) {
			// A 422 from reference validation, already shaped. Returning it
			// unchanged keeps the message identical between create and update.
			return nil, err
		}
```

Place that check **before** the `isUniqueViolation` branches, or a validation 422 raised on a retry attempt will be misread as a slug collision.

- [ ] **Step 6: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -v` Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal
git commit -m "feat(api): accept folder and tag references on link create"
```

---

### Task 11: Folder and tag changes on link update

**Files:**

- Modify: `apps/api/internal/api/links.go`
- Modify: `apps/api/internal/api/links_test.go`

**Interfaces:**

- Consumes: Task 10's `resolveFolderRef` and `resolveTagRefs`; Task 7's `DeleteLinkTags` and `InsertLinkTags`.
- Produces: nothing new for later tasks.

**The one subtlety.** A `*uuid.UUID` body field cannot distinguish "`folder_id` was omitted" from "`folder_id` was sent as `null`", so a pointer alone gives no way to unfile a link. The typed `Body` stays — the OpenAPI schema and the generated TS client depend on it — and Huma's `RawBody []byte` is added alongside to check key presence. `tag_ids` needs none of this: an omitted array unmarshals to a nil slice and `[]` to an empty non-nil one, which already distinguishes them.

`expires_at` has the identical limitation and is left alone; fixing it is a change to plan 3's surface and belongs in its own task.

- [ ] **Step 1: Write the failing tests**

Append to `apps/api/internal/api/links_test.go`:

```go
func TestUpdateLinkReplacesTheWholeTagSet(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	a := h.createTag(t, token, team.ID, "Alpha")
	b := h.createTag(t, token, team.ID, "Beta")
	c := h.createTag(t, token, team.ID, "Gamma")
	link := h.createLinkWithTags(t, token, team.ID, "https://example.org/x", a.ID, b.ID)

	resp := h.patch(t, token, "/v1/links/"+link.ID.String(), map[string]any{
		"tag_ids": []string{a.ID.String(), c.ID.String()},
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", resp.Code, resp.Body.String())
	}
	var body Link
	h.decode(t, resp, &body)
	if len(body.Tags) != 2 || body.Tags[0].Name != "Alpha" || body.Tags[1].Name != "Gamma" {
		t.Fatalf("got %v, want Alpha and Gamma", body.Tags)
	}
}

func TestUpdateLinkWithEmptyTagIDsDetachesEverything(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	tag := h.createTag(t, token, team.ID, "Presse")
	link := h.createLinkWithTags(t, token, team.ID, "https://example.org/x", tag.ID)

	resp := h.patch(t, token, "/v1/links/"+link.ID.String(), map[string]any{
		"tag_ids": []string{},
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}
	var joins int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from link_tag where link_id = $1`, link.ID).Scan(&joins); err != nil {
		t.Fatalf("query: %v", err)
	}
	if joins != 0 {
		t.Fatalf("got %d link_tag rows, want 0", joins)
	}
}

func TestUpdateLinkWithoutTagIDsLeavesTagsAlone(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	tag := h.createTag(t, token, team.ID, "Presse")
	link := h.createLinkWithTags(t, token, team.ID, "https://example.org/x", tag.ID)

	resp := h.patch(t, token, "/v1/links/"+link.ID.String(), map[string]any{
		"destination_url": "https://example.org/y",
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}
	var joins int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from link_tag where link_id = $1`, link.ID).Scan(&joins); err != nil {
		t.Fatalf("query: %v", err)
	}
	if joins != 1 {
		t.Fatalf("got %d link_tag rows, want the tag untouched", joins)
	}
}

func TestUpdateLinkWithNullFolderIDUnfilesIt(t *testing.T) {
	// The distinction this asserts is why the handler reads RawBody: an
	// omitted folder_id and an explicit null are both a nil pointer.
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	folder := h.createFolder(t, token, team.ID, "Sommerfest")
	link := h.createLinkInFolder(t, token, team.ID, "https://example.org/x", folder.ID)

	resp := h.patchRaw(t, token, "/v1/links/"+link.ID.String(), `{"folder_id": null}`)

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", resp.Code, resp.Body.String())
	}
	var body Link
	h.decode(t, resp, &body)
	if body.FolderID != nil {
		t.Fatalf("got folder_id %v, want null", *body.FolderID)
	}
}

func TestUpdateLinkWithoutFolderIDLeavesItFiled(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	folder := h.createFolder(t, token, team.ID, "Sommerfest")
	link := h.createLinkInFolder(t, token, team.ID, "https://example.org/x", folder.ID)

	resp := h.patch(t, token, "/v1/links/"+link.ID.String(), map[string]any{
		"destination_url": "https://example.org/y",
	})

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}
	var body Link
	h.decode(t, resp, &body)
	if body.FolderID == nil || *body.FolderID != folder.ID {
		t.Fatalf("got folder_id %v, want it unchanged at %s", body.FolderID, folder.ID)
	}
}

func TestUpdateLinkRecordsFolderAndTagChangesInOneAuditRow(t *testing.T) {
	// One row per PATCH, not one per changed field: a single request is a
	// single request, and metadata.changed says which fields moved.
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	folder := h.createFolder(t, token, team.ID, "Sommerfest")
	tag := h.createTag(t, token, team.ID, "Presse")
	link := h.createLink(t, token, team.ID, "https://example.org/x")

	resp := h.patch(t, token, "/v1/links/"+link.ID.String(), map[string]any{
		"folder_id": folder.ID.String(),
		"tag_ids":   []string{tag.ID.String()},
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}

	var rows int
	if err := h.Pool.QueryRow(t.Context(),
		`select count(*) from audit_log where entity_id = $1 and action = 'link.updated'`,
		link.ID).Scan(&rows); err != nil {
		t.Fatalf("query: %v", err)
	}
	if rows != 1 {
		t.Fatalf("got %d audit rows, want exactly 1", rows)
	}

	var changed []string
	if err := h.Pool.QueryRow(t.Context(),
		`select array(select jsonb_array_elements_text(metadata->'changed'))
		 from audit_log where entity_id = $1 and action = 'link.updated'`,
		link.ID).Scan(&changed); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !slices.Contains(changed, "folder_id") || !slices.Contains(changed, "tags") {
		t.Fatalf("got changed %v, want folder_id and tags both listed", changed)
	}
}
```

Add `patchRaw` to `testhelper_test.go` — the same as `patch` but taking a raw JSON string, so a test can send an explicit `null`.

- [ ] **Step 2: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run TestUpdateLink -v` Expected: FAIL on the new cases; the existing `TestUpdateLink*` cases still pass.

- [ ] **Step 3: Extend the input**

```go
type UpdateLinkInput struct {
	authz.LinkEditorScope
	// RawBody is read only to tell an omitted folder_id from an explicit null;
	// a *uuid.UUID is nil for both, so a pointer alone gives no way to unfile a
	// link. The typed Body below stays the source of every value — the
	// OpenAPI schema and the generated TS client depend on it.
	RawBody []byte
	Body    struct {
		DestinationURL   *string     `json:"destination_url,omitempty" maxLength:"2048"`
		Slug             *string     `json:"slug,omitempty" maxLength:"64"`
		RedirectType     *int        `json:"redirect_type,omitempty" enum:"301,302"`
		State            *string     `json:"state,omitempty" enum:"active,disabled" doc:"expired follows from expires_at and flagged is set by scanning; neither is a caller's to write."`
		ExpiresAt        *time.Time  `json:"expires_at,omitempty"`
		AnalyticsEnabled *bool       `json:"analytics_enabled,omitempty"`
		FolderID         *uuid.UUID  `json:"folder_id,omitempty" nullable:"true" doc:"Send null to unfile the link."`
		TagIDs           []uuid.UUID `json:"tag_ids,omitempty" maxItems:"10" doc:"Replaces the whole tag set. Send [] to remove every tag."`
	}
}

// bodyHasKey reports whether the request body carried this key at all,
// which is the difference between "leave it alone" and "set it to null".
func bodyHasKey(raw []byte, key string) bool {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		return false
	}
	_, ok := keys[key]
	return ok
}
```

Add `"encoding/json"` to the imports.

- [ ] **Step 4: Handle both fields in updateLink**

Inside the existing transaction closure, after the other field comparisons and before the `len(changed) == 0` check:

```go
		if bodyHasKey(in.RawBody, "folder_id") {
			folderID, err := d.resolveFolderRef(ctx, q, member.TeamID, in.Body.FolderID)
			if err != nil {
				return err
			}
			if !equalUUIDPtr(folderID, before.FolderID) {
				params.FolderID = folderID
				changed = append(changed, "folder_id")
				metadata["folder_id"] = map[string]any{"from": before.FolderID, "to": folderID}
			}
		} else {
			params.FolderID = before.FolderID
		}
```

and, after the `q.UpdateLink` call so the link is known to exist:

```go
		if in.Body.TagIDs != nil {
			tags, err := d.resolveTagRefs(ctx, q, member.TeamID, in.Body.TagIDs)
			if err != nil {
				return err
			}
			if err := q.DeleteLinkTags(ctx, row.ID); err != nil {
				return err
			}
			if len(tags) > 0 {
				ids := make([]uuid.UUID, 0, len(tags))
				for _, tag := range tags {
					ids = append(ids, tag.ID)
				}
				if err := q.InsertLinkTags(ctx, db.InsertLinkTagsParams{
					LinkID: row.ID, TagIds: ids,
				}); err != nil {
					return err
				}
			}
			changed = append(changed, "tags")
			metadata["tags"] = map[string]any{"count": len(tags)}
			updatedTags = tags
		}
```

`metadata["tags"]` records a count rather than the names: a tag name is user-supplied free text and the audit metadata denylist exists precisely to keep unvetted strings out of that column. Declare `updatedTags []Tag` beside `updated`, and add:

```go
// equalUUIDPtr compares two optional UUIDs, treating two nils as equal.
func equalUUIDPtr(a, b *uuid.UUID) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}
```

Note the ordering constraint: `tag_ids` is handled **after** `q.UpdateLink`, but a PATCH carrying only `tag_ids` changes no link column, so `len(changed) == 0` would short-circuit before the update runs. Restructure the no-op check to account for it — compute the tag change first into a local `tagsChanging bool`, and treat `len(changed) == 0 && !tagsChanging` as the no-op case. A PATCH that only replaces tags must still write its audit row and must still return 200.

- [ ] **Step 5: Run the tests**

Run: `cd apps/api && go test ./internal/api/ -v` Expected: PASS, including every pre-existing link test.

- [ ] **Step 6: Confirm the hot path is untouched**

Run: `cd apps/api && go test ./internal/api/ -run TestRedirect -v` Expected: PASS. No folder or tag write calls `invalidateLink`, and none should have been added — `link.Cached` carries no folder or tag field.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal
git commit -m "feat(api): accept folder and tag changes on link update"
```

---

### Task 12: List filters and the tag stitch

**Files:**

- Modify: `apps/api/internal/db/queries/link_crud.sql`
- Modify: `apps/api/internal/api/links.go`
- Modify: `apps/api/internal/api/links_test.go`

**Interfaces:**

- Consumes: Task 7's `ListTagsForLinks`.
- Produces: the final `Link` representation with `folder_id` and `tags`, and the `folder_id` and `tag_id` list filters.

- [ ] **Step 1: Add the filters to the list query**

In `ListLinksForTeam` and `CountLinksForTeam`, add two optional filters alongside the existing ones, following exactly how `$q`, `$state` and `$domain_id` are written there:

```sql
  and (sqlc.narg('folder_id')::uuid is null or l.folder_id = sqlc.narg('folder_id')::uuid)
  and (sqlc.narg('tag_id')::uuid is null or exists (
        select 1 from link_tag lt
        where lt.link_id = l.id and lt.tag_id = sqlc.narg('tag_id')::uuid))
```

The tag filter is an `exists` subquery rather than a join, for the same reason the tag read is a second query: a join multiplies rows before `LIMIT`.

Run `sqlc generate`.

- [ ] **Step 2: Write the failing tests**

```go
func TestListLinksFiltersByFolder(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	folder := h.createFolder(t, token, team.ID, "Sommerfest")
	filed := h.createLinkInFolder(t, token, team.ID, "https://example.org/in", folder.ID)
	h.createLink(t, token, team.ID, "https://example.org/out")

	resp := h.get(t, token,
		"/v1/teams/"+team.ID.String()+"/links?folder_id="+folder.ID.String())

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}
	var page Page[Link]
	h.decode(t, resp, &page)
	if len(page.Items) != 1 || page.Items[0].ID != filed.ID {
		t.Fatalf("got %d links, want only the filed one", len(page.Items))
	}
	if page.TotalCount != 1 {
		t.Fatalf("got total %d, want 1 — the count must respect the filter", page.TotalCount)
	}
}

func TestListLinksFiltersByTag(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	tag := h.createTag(t, token, team.ID, "Presse")
	tagged := h.createLinkWithTags(t, token, team.ID, "https://example.org/in", tag.ID)
	h.createLink(t, token, team.ID, "https://example.org/out")

	resp := h.get(t, token, "/v1/teams/"+team.ID.String()+"/links?tag_id="+tag.ID.String())

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.Code)
	}
	var page Page[Link]
	h.decode(t, resp, &page)
	if len(page.Items) != 1 || page.Items[0].ID != tagged.ID {
		t.Fatalf("got %d links, want only the tagged one", len(page.Items))
	}
}

func TestListLinksDoesNotMultiplyRowsForMultipleTags(t *testing.T) {
	// The reason the tag read is a second query and the filter is an exists
	// subquery: a join would return this link three times, and LIMIT would
	// then hide other links behind the duplicates.
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	a := h.createTag(t, token, team.ID, "Alpha")
	b := h.createTag(t, token, team.ID, "Beta")
	c := h.createTag(t, token, team.ID, "Gamma")
	h.createLinkWithTags(t, token, team.ID, "https://example.org/x", a.ID, b.ID, c.ID)

	resp := h.get(t, token, "/v1/teams/"+team.ID.String()+"/links")

	var page Page[Link]
	h.decode(t, resp, &page)
	if len(page.Items) != 1 {
		t.Fatalf("got %d rows, want 1 link", len(page.Items))
	}
	if page.TotalCount != 1 {
		t.Fatalf("got total %d, want 1", page.TotalCount)
	}
	if len(page.Items[0].Tags) != 3 {
		t.Fatalf("got %d tags, want 3", len(page.Items[0].Tags))
	}
}

func TestListLinksReturnsEachLinksOwnTags(t *testing.T) {
	// The stitch must key by link_id; a bug that assigns every tag to every
	// link would pass a single-link test.
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	a := h.createTag(t, token, team.ID, "Alpha")
	b := h.createTag(t, token, team.ID, "Beta")
	first := h.createLinkWithTags(t, token, team.ID, "https://example.org/1", a.ID)
	second := h.createLinkWithTags(t, token, team.ID, "https://example.org/2", b.ID)

	resp := h.get(t, token, "/v1/teams/"+team.ID.String()+"/links")
	var page Page[Link]
	h.decode(t, resp, &page)

	byID := map[uuid.UUID][]Tag{}
	for _, item := range page.Items {
		byID[item.ID] = item.Tags
	}
	if len(byID[first.ID]) != 1 || byID[first.ID][0].Name != "Alpha" {
		t.Fatalf("first link got %v, want only Alpha", byID[first.ID])
	}
	if len(byID[second.ID]) != 1 || byID[second.ID][0].Name != "Beta" {
		t.Fatalf("second link got %v, want only Beta", byID[second.ID])
	}
}

func TestGetLinkReturnsItsTags(t *testing.T) {
	h := newTestHarness(t)
	team := h.newTeam(t, "Verein")
	token := h.tokenFor(t, team.OwnerID)
	tag := h.createTag(t, token, team.ID, "Presse")
	link := h.createLinkWithTags(t, token, team.ID, "https://example.org/x", tag.ID)

	resp := h.get(t, token, "/v1/links/"+link.ID.String())

	var body Link
	h.decode(t, resp, &body)
	if len(body.Tags) != 1 || body.Tags[0].Name != "Presse" {
		t.Fatalf("got %v, want one tag named Presse", body.Tags)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `cd apps/api && go test ./internal/api/ -run 'TestListLinks|TestGetLinkReturns' -v` Expected: FAIL.

- [ ] **Step 4: Add the stitch**

```go
// attachTags fills in the Tags of a page of links with one extra query, not a
// join. A left join onto the list query would multiply rows before LIMIT
// applies, so a page of 20 links would silently return fewer.
func (d Deps) attachTags(ctx context.Context, teamID uuid.UUID, links []Link) error {
	if len(links) == 0 {
		return nil
	}

	ids := make([]uuid.UUID, 0, len(links))
	for _, l := range links {
		ids = append(ids, l.ID)
	}

	rows, err := d.Queries.ListTagsForLinks(ctx, db.ListTagsForLinksParams{
		LinkIds: ids, TeamID: teamID,
	})
	if err != nil {
		return fmt.Errorf("load tags for links: %w", err)
	}

	byLink := make(map[uuid.UUID][]Tag, len(links))
	for _, row := range rows {
		byLink[row.LinkID] = append(byLink[row.LinkID], Tag{
			ID: row.ID, TeamID: teamID, Name: row.Name,
		})
	}
	for i := range links {
		// A nil slice would marshal as null; an empty one as []. A client
		// iterating tags should never have to nil-check.
		if tags, ok := byLink[links[i].ID]; ok {
			links[i].Tags = tags
		} else {
			links[i].Tags = []Tag{}
		}
	}
	return nil
}
```

Call it in `listLinks` after building `items`, and in `getLink` with a one-element slice. In `createLink` and `updateLink`, set `Tags` from the already-resolved `createdTags`/`updatedTags` rather than re-querying — those paths know the answer.

- [ ] **Step 5: Add the query parameters**

```go
	FolderID string `query:"folder_id" doc:"Restrict to one folder, as a UUID."`
	TagID    string `query:"tag_id" doc:"Restrict to links carrying one tag, as a UUID."`
```

Both are `string`, not `*uuid.UUID`: Huma v2 panics on a pointer-typed query parameter. Parse each with `uuid.Parse` in the handler and answer 422 on failure, exactly as `DomainID` already does two lines above.

- [ ] **Step 6: Run the whole suite**

Run: `cd apps/api && go test ./... -v` Expected: PASS, every package, no skips.

- [ ] **Step 7: Commit**

```bash
git add apps/api/internal
git commit -m "feat(api): filter links by folder and tag"
```

---

### Task 13: Isolation, falsification and the documentation amendments

**Files:**

- Create: `apps/api/internal/api/organization_isolation_test.go`
- Modify: `docs/planning/05-database-schema.md`
- Modify: `docs/planning/06-api-design.md`

**Interfaces:**

- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Write the isolation tests**

Create `apps/api/internal/api/organization_isolation_test.go`, mirroring `links_isolation_test.go`. Every case asserts **persisted state**, not only the response status — a handler that returns 404 and writes anyway would pass a status-only test:

```go
package api

import (
	"net/http"
	"testing"
)

func TestAnotherTeamsFoldersAreInvisible(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	h.createFolder(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.get(t, h.tokenFor(t, mine.OwnerID), "/v1/teams/"+mine.ID.String()+"/folders")

	var page Page[Folder]
	h.decode(t, resp, &page)
	if len(page.Items) != 0 {
		t.Fatalf("got %d folders, want none of another team's", len(page.Items))
	}
}

func TestAnotherTeamsFolderCannotBeRenamed(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	foreign := h.createFolder(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.patch(t, h.tokenFor(t, mine.OwnerID), "/v1/folders/"+foreign.ID.String(),
		map[string]any{"name": "Gekapert"})

	if resp.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.Code)
	}

	var name string
	if err := h.Pool.QueryRow(t.Context(),
		`select name from folder where id = $1`, foreign.ID).Scan(&name); err != nil {
		t.Fatalf("query: %v", err)
	}
	if name != "Fremd" {
		t.Fatalf("got %q — the row was modified despite the 404", name)
	}
}

func TestAnotherTeamsFolderCannotBeDeleted(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	foreign := h.createFolder(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.delete(t, h.tokenFor(t, mine.OwnerID), "/v1/folders/"+foreign.ID.String())

	if resp.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.Code)
	}
	var exists bool
	if err := h.Pool.QueryRow(t.Context(),
		`select exists(select 1 from folder where id = $1)`, foreign.ID).Scan(&exists); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatalf("the row was deleted despite the 404")
	}
}
```

and the matching tag cases in the same file:

```go
func TestAnotherTeamsTagsAreInvisible(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	h.createTag(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.get(t, h.tokenFor(t, mine.OwnerID), "/v1/teams/"+mine.ID.String()+"/tags")

	var page Page[Tag]
	h.decode(t, resp, &page)
	if len(page.Items) != 0 {
		t.Fatalf("got %d tags, want none of another team's", len(page.Items))
	}
}

func TestAnotherTeamsTagCannotBeRenamed(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	foreign := h.createTag(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.patch(t, h.tokenFor(t, mine.OwnerID), "/v1/tags/"+foreign.ID.String(),
		map[string]any{"name": "Gekapert"})

	if resp.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.Code)
	}

	var name string
	if err := h.Pool.QueryRow(t.Context(),
		`select name from tag where id = $1`, foreign.ID).Scan(&name); err != nil {
		t.Fatalf("query: %v", err)
	}
	if name != "Fremd" {
		t.Fatalf("got %q — the row was modified despite the 404", name)
	}
}

func TestAnotherTeamsTagCannotBeDeleted(t *testing.T) {
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	foreign := h.createTag(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")

	resp := h.delete(t, h.tokenFor(t, mine.OwnerID), "/v1/tags/"+foreign.ID.String())

	if resp.Code != http.StatusNotFound {
		t.Fatalf("got %d, want 404", resp.Code)
	}
	var exists bool
	if err := h.Pool.QueryRow(t.Context(),
		`select exists(select 1 from tag where id = $1)`, foreign.ID).Scan(&exists); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !exists {
		t.Fatalf("the row was deleted despite the 404")
	}
}

func TestFilteringByAnotherTeamsTagReturnsNothing(t *testing.T) {
	// The filter is a plain value, not an addressed resource, so this is an
	// empty page rather than a 404 — and it must not become a 500 either.
	h := newTestHarness(t)
	mine := h.newTeam(t, "Mein Verein")
	theirs := h.newTeam(t, "Fremder Verein")
	token := h.tokenFor(t, mine.OwnerID)
	foreign := h.createTag(t, h.tokenFor(t, theirs.OwnerID), theirs.ID, "Fremd")
	h.createLink(t, token, mine.ID, "https://example.org/mine")

	resp := h.get(t, token,
		"/v1/teams/"+mine.ID.String()+"/links?tag_id="+foreign.ID.String())

	if resp.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", resp.Code, resp.Body.String())
	}
	var page Page[Link]
	h.decode(t, resp, &page)
	if len(page.Items) != 0 {
		t.Fatalf("got %d links, want none", len(page.Items))
	}
}
```

- [ ] **Step 2: Run them**

Run: `cd apps/api && go test ./internal/api/ -run 'TestAnotherTeams' -v` Expected: PASS.

- [ ] **Step 3: Falsify each property**

For each property below, **mutate** the code so the behaviour is wrong, run the named test, confirm it fails, then restore. Mutation, never deletion: deleting the code proves the compiler noticed a dangling reference, not that a test noticed the behaviour.

| Mutate | Expect this to fail |
| --- | --- |
| `GetFolderInTeam`: change `where team_id = $1 and id = $2` to `where id = $2` | `TestCreateLinkRejectsAnotherTeamsFolder` |
| `ListTagsByIDs`: drop the `team_id = $1` clause | `TestCreateLinkRejectsAnotherTeamsTag` |
| `UpdateFolder`: drop `and team_id = $2` | `TestAnotherTeamsFolderCannotBeRenamed` |
| `DeleteFolder`: drop `and team_id = $2` | `TestAnotherTeamsFolderCannotBeDeleted` |
| `UpdateTag`: drop `and team_id = $2` | `TestAnotherTeamsTagCannotBeRenamed` |
| `DeleteTag`: drop `and team_id = $2` | `TestAnotherTeamsTagCannotBeDeleted` |
| `updateLink`: skip `q.DeleteLinkTags` before inserting | `TestUpdateLinkReplacesTheWholeTagSet` |
| `bodyHasKey`: always return `false` | `TestUpdateLinkWithNullFolderIDUnfilesIt` |
| `bodyHasKey`: always return `true` | `TestUpdateLinkWithoutFolderIDLeavesItFiled` |
| The migration: recreate the index without `lower()` | `TestCreateTagRejectsACaseInsensitiveDuplicate` |
| `createFolder`: change `count >= maxFoldersPerTeam` to `count > maxFoldersPerTeam` | `TestCreateFolderEnforcesTheTeamCap` |
| `createTag`: same off-by-one | `TestCreateTagEnforcesTheTeamCap` |
| `resolveTagRefs`: drop the `len(tagIDs) > maxTagsPerLink` check | `TestCreateLinkEnforcesTheTagsPerLinkCap` |
| `attachTags`: assign every tag to every link | `TestListLinksReturnsEachLinksOwnTags` |
| `resolveMembership`: restore the unchecked `claimsUserID` | `TestResolveMembershipRefusesWithoutClaims` |

Any row where the test still passes is a test that does not test what it claims. Fix the test, not the table.

- [ ] **Step 4: Amend doc 05**

In `docs/planning/05-database-schema.md`, change the `parent_folder_id` annotation from "nullable, allows nesting" to record the decision:

```
  parent_folder_id  uuid references folder(id),          -- nullable; unused, see below
```

and add below the table:

> **Folders are flat.** `parent_folder_id` is present but never written by the API — the decision and its four rejected obligations are in `docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`. Keeping the column means nesting arrives later as an API change rather than a migration.

- [ ] **Step 5: Amend doc 06**

In `docs/planning/06-api-design.md`, under the tags section, record how tags attach:

> Tags attach to a link through `tag_ids` on `POST`/`PATCH /v1/links`, as a whole-set replacement — not through a subresource. An omitted array leaves the tags untouched, `[]` detaches every tag. The reasoning, including the rejected `PUT/DELETE /v1/links/{link_id}/tags/{tag_id}` pair, is in `docs/superpowers/specs/2026-09-03-folders-and-tags-design.md`.

Also add the six new audit actions to the taxonomy list if that document carries one.

- [ ] **Step 6: Run everything**

```bash
cd apps/api && go vet ./... && go test ./... && golangci-lint run
cd ../.. && pnpm format:check && pnpm lint && pnpm typecheck
```

Expected: all clean, no skipped Go tests.

- [ ] **Step 7: Regenerate the API client**

The OpenAPI surface changed — eight new operations and two new link fields. Regenerate `packages/api-client` by whatever script the repo defines for it, and commit the result.

- [ ] **Step 7: Commit**

```bash
git add apps/api docs packages
git commit -m "test(api): assert folder and tag isolation"
```

---

## Notes for the executor

**Read before Task 5.** `apps/api/internal/authz/link.go` is the pattern every entity scope in this plan copies. The asymmetry that matters: a non-member and a missing entity both get 404 so IDs cannot be probed, while a member whose role is too low gets 403 because that caller already knows the entity exists. Getting this backwards is a disclosure bug that no test in the matrix will catch for you.

**The permission matrix cannot see a missing `team_id`.** `matrix_test.go` observes HTTP status per operation and role. A handler with the right scope embed whose SQL forgets its tenancy filter passes it cleanly. That is why every query in Tasks 4 and 7 carries a visible `team_id` filter, why the isolation tests in Task 13 assert persisted state, and why the falsification table exists.

**One deliberate exception.** `GetFolderScope` and `GetTagScope` do not filter by `team_id`. They are what _discovers_ the team, so they cannot filter by the answer — the same exception `GetLinkScope` already documents. Every query the handler runs afterwards is filtered by the team those return.

**Do not add a cache invalidation.** `link.Cached` carries no folder or tag field, so no write in this plan affects the redirect path. The instinct to call `invalidateLink` after a folder change is understandable — plan 3 established that link writes invalidate — and it would cost a Redis command per organizational edit and buy nothing. If a later change puts a folder or tag into the cached payload, that changes.

**Tasks 4 and 7 are independent of Tasks 5 and 8** in the other resource's chain, so folder work (4→5→6) and tag work (7→8→9) can proceed in parallel. Tasks 10–12 depend on both. Task 3 must land before 5 and 8.
