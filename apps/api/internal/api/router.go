package api

import (
	"encoding/json"
	"net/http"

	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// NewRouter builds the single handler the server runs. Two surfaces share one
// process, separated by hostname: the JSON API answers only on the configured
// API hostname, and every other hostname is treated as a short-link domain.
//
// The split is what keeps /v1 off a team's custom domain and keeps the
// redirect routes off the API's own domain, where a slug could otherwise
// shadow a future API path.
func NewRouter(deps Deps) http.Handler {
	apiHost := Hostname(deps.Config.APIHostname)

	apiSurface := chi.NewRouter()
	// Not middleware.RealIP: it is deprecated as vulnerable to IP spoofing
	// (GHSA-3fxj-6jh8-hvhx), and no /v1 handler reads r.RemoteAddr anyway —
	// they don't need a client IP at all.
	apiSurface.Use(middleware.Recoverer)
	deps.RegisterV1(humachi.New(apiSurface, NewHumaConfig()))

	redirectSurface := chi.NewRouter()
	redirectSurface.Use(middleware.Recoverer)
	redirectSurface.Get("/{slug}", deps.HandleRedirect)
	redirectSurface.Get("/{slug}/verify", deps.HandleVerifyForm)
	redirectSurface.Post("/{slug}/verify", deps.HandleVerifySubmit)

	root := chi.NewRouter()
	// /health answers on every hostname: the uptime monitor and the platform's
	// own checks do not know which one they are hitting.
	root.Get("/health", plainHealth)
	root.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		if Hostname(r.Host) == apiHost {
			apiSurface.ServeHTTP(w, r)
			return
		}
		redirectSurface.ServeHTTP(w, r)
	})

	return root
}

func plainHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
