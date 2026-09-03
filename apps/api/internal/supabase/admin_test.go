package supabase_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/supabase"
)

func TestInviteUserPostsToTheAuthInviteEndpoint(t *testing.T) {
	invitedID := uuid.New()

	var gotPath, gotAPIKey, gotAuth string
	var gotBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("apikey")
		gotAuth = r.Header.Get("Authorization")

		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": invitedID.String()})
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "service-role-key")
	require.NoError(t, err)

	id, err := client.InviteUser(context.Background(), "neu@verein.test",
		map[string]any{"team_id": "abc", "role": "editor"})

	require.NoError(t, err)
	require.Equal(t, invitedID, id)
	require.Equal(t, "/invite", gotPath)
	require.Equal(t, "service-role-key", gotAPIKey)
	require.Equal(t, "Bearer service-role-key", gotAuth)
	require.Equal(t, "neu@verein.test", gotBody["email"])
	require.Equal(t, map[string]any{"team_id": "abc", "role": "editor"}, gotBody["data"])
}

func TestInviteUserReportsAnExistingAddressDistinctly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code":       422,
			"error_code": "email_exists",
			"msg":        "A user with this email address has already been registered",
		})
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "key")
	require.NoError(t, err)

	_, err = client.InviteUser(context.Background(), "alt@verein.test", nil)

	require.ErrorIs(t, err, supabase.ErrUserExists,
		"the caller falls back to the direct-add branch for an address that already has an account")
}

func TestInviteUserSurfacesAServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "key")
	require.NoError(t, err)

	_, err = client.InviteUser(context.Background(), "neu@verein.test", nil)

	require.Error(t, err)
	require.NotErrorIs(t, err, supabase.ErrUserExists)
}

func TestNewClientRefusesAnIncompleteConfiguration(t *testing.T) {
	_, err := supabase.NewClient("", "key")
	require.ErrorIs(t, err, supabase.ErrNotConfigured)

	_, err = supabase.NewClient("https://project.supabase.co/auth/v1", "")
	require.ErrorIs(t, err, supabase.ErrNotConfigured)
}

func TestInviteUserDoesNotLeakTheServiceRoleKeyIntoErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("upstream exploded"))
	}))
	t.Cleanup(server.Close)

	client, err := supabase.NewClient(server.URL, "super-secret-service-role-key")
	require.NoError(t, err)

	_, err = client.InviteUser(context.Background(), "neu@verein.test", nil)

	require.Error(t, err)
	require.NotContains(t, err.Error(), "super-secret-service-role-key",
		"the service-role key bypasses every database policy; it must never reach a log line")
}
