package authz_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestRolesAreOrderedViewerToOwner(t *testing.T) {
	require.True(t, authz.RoleOwner.AtLeast(authz.RoleAdmin))
	require.True(t, authz.RoleAdmin.AtLeast(authz.RoleEditor))
	require.True(t, authz.RoleEditor.AtLeast(authz.RoleViewer))
	require.True(t, authz.RoleViewer.AtLeast(authz.RoleViewer))

	require.False(t, authz.RoleViewer.AtLeast(authz.RoleEditor))
	require.False(t, authz.RoleEditor.AtLeast(authz.RoleAdmin))
	require.False(t, authz.RoleAdmin.AtLeast(authz.RoleOwner))
}

func TestAnUnknownRoleSatisfiesNothing(t *testing.T) {
	unknown := authz.Role("superuser")

	require.False(t, unknown.AtLeast(authz.RoleViewer),
		"an unrecognised role must never pass a check — failing open is a data leak")
}

func TestParseRoleAcceptsExactlyTheFourSchemaRoles(t *testing.T) {
	for _, name := range []string{"viewer", "editor", "admin", "owner"} {
		role, err := authz.ParseRole(name)
		require.NoError(t, err)
		require.Equal(t, name, role.String())
	}
}

func TestParseRoleRejectsAnythingElse(t *testing.T) {
	for _, name := range []string{"", "Owner", "root", "admin "} {
		_, err := authz.ParseRole(name)
		require.ErrorIs(t, err, authz.ErrInvalidRole, "input %q", name)
	}
}
