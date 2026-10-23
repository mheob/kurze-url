package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogoutUsesScopeLocal(t *testing.T) {
	srv := newAuthServer(t)

	err := Logout(context.Background(), srv.Client(), srv.URL, testAPIKey, "the-access-token")

	require.NoError(t, err)
	calls := srv.callsTo(stubLogoutPath)
	require.Len(t, calls, 1)
	require.Equal(t, http.MethodPost, calls[0].Method)
	// Local scope ends this CLI's session and no other: not the browser's,
	// not another machine's.
	require.Equal(t, "local", calls[0].Query.Get("scope"))
	require.Len(t, calls[0].Query, 1)
	require.Equal(t, "Bearer the-access-token", calls[0].Authorization)
	require.Equal(t, testAPIKey, calls[0].Apikey)
}

func TestLogoutAcceptsAny2xx(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusNoContent, http.StatusAccepted} {
		srv := newAuthServer(t)
		srv.logoutStatus = status

		require.NoError(t, Logout(context.Background(), srv.Client(), srv.URL, testAPIKey, "the-access-token"), status)
	}
}

func TestLogoutReportsOnlyTheStatus(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusInternalServerError} {
		srv := newAuthServer(t)
		srv.logoutStatus = status

		err := Logout(context.Background(), srv.Client(), srv.URL, testAPIKey, "the-access-token")

		require.Error(t, err)
		require.Contains(t, err.Error(), http.StatusText(status))
		require.NotContains(t, err.Error(), "the-access-token")
	}
}

func TestLogoutReportsANetworkError(t *testing.T) {
	srv := newAuthServer(t)
	srv.Close()

	err := Logout(context.Background(), srv.Client(), srv.URL, testAPIKey, "the-access-token")

	require.Error(t, err)
	require.NotContains(t, err.Error(), "the-access-token")
}
