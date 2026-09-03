package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mheob/kurze-url/apps/api/internal/auth"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

// NewHumaConfig builds the OpenAPI 3.1 document config. The bearer scheme is
// declared once here; individual operations opt in via Security, and the
// middleware enforces it only where declared.
func NewHumaConfig() huma.Config {
	config := huma.DefaultConfig("kurze-url API", "1.0.0")
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearerAuth": {
			Type:         "http",
			Scheme:       "bearer",
			BearerFormat: "JWT",
			Description:  "A Supabase-issued access token (ES256, verified against the project's JWKS).",
		},
	}
	return config
}

// HealthOutput is the body of GET /v1/health.
type HealthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// RegisterV1 mounts the versioned JSON API. Only routes registered here appear
// in the OpenAPI document; the redirect surface is deliberately absent.
func (d Deps) RegisterV1(api huma.API) {
	api.UseMiddleware(d.authMiddleware(api))

	huma.Register(api, huma.Operation{
		OperationID: "get-health",
		Method:      http.MethodGet,
		Path:        "/v1/health",
		Summary:     "Liveness probe",
		Tags:        []string{"Meta"},
	}, func(_ context.Context, _ *struct{}) (*HealthOutput, error) {
		out := &HealthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	d.registerMe(api)
	d.registerTeams(api)
	d.registerMembers(api)
	d.registerAuditLog(api)
}

// authMiddleware enforces the bearer scheme on exactly the operations that
// declare it. Operations without a Security block stay open by design.
func (d Deps) authMiddleware(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if !requiresBearer(ctx.Operation()) {
			next(ctx)
			return
		}

		if d.Verifier == nil {
			d.Log.Error("authenticated operation reached with no JWT verifier configured",
				"operation", ctx.Operation().OperationID)
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "authentication is not configured")
			return
		}

		token := bearerToken(ctx.Header("Authorization"))
		if token == "" {
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "missing bearer token")
			return
		}

		claims, err := d.Verifier.Verify(ctx.Context(), token)
		if err != nil {
			// The reason is logged, never returned: a precise error tells an
			// attacker which part of the token to fix.
			d.Log.Warn("bearer token rejected", "error", err)
			_ = huma.WriteErr(api, ctx, http.StatusUnauthorized, "invalid bearer token")
			return
		}

		inner := auth.WithClaims(ctx.Context(), claims)
		if d.Queries != nil {
			inner = authz.WithResolver(inner, authz.NewQueryResolver(d.Queries))
		}
		next(huma.WithContext(ctx, inner))
	}
}

func requiresBearer(operation *huma.Operation) bool {
	for _, scheme := range operation.Security {
		if _, ok := scheme["bearerAuth"]; ok {
			return true
		}
	}
	return false
}

func bearerToken(header string) string {
	scheme, token, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return ""
	}
	return strings.TrimSpace(token)
}

// UserFromContext returns the verified claims a bearer-authenticated operation
// was called with. It is a thin wrapper over auth.ClaimsFromContext so handlers
// need not import internal/auth for this one call.
func UserFromContext(ctx context.Context) (auth.Claims, bool) {
	return auth.ClaimsFromContext(ctx)
}
