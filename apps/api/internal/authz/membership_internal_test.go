package authz

import (
	"context"
	"errors"
	"net/http"
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
	// UseMiddleware must run before Register: huma.Register snapshots
	// api.Middlewares() and bakes it into the operation's handler chain right
	// there (huma.go's Register calls api.Middlewares().Handler(...)), so a
	// middleware added afterward is silently never part of that chain.
	api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		// No auth.WithClaims: the caller is unauthenticated. A resolver IS
		// installed, so reaching it would be the bug.
		inner := WithResolver(ctx.Context(), refusingResolver{t: t})
		var out Membership
		got = resolveMembership(huma.WithContext(ctx, inner),
			uuid.New(), RoleViewer, "team not found", &out)
		next(ctx)
	})

	huma.Register(api, huma.Operation{
		OperationID: "probe",
		Method:      http.MethodGet,
		Path:        "/probe",
	}, func(_ context.Context, _ *struct{}) (*struct{}, error) {
		return &struct{}{}, nil
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
