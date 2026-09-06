package api_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
)

func TestClaimDomainReturnsTheRecordsToCreate(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decode[api.Domain](t, rec)
	require.Equal(t, "links.verein.test", body.Hostname)
	require.Equal(t, "pending", body.VerificationStatus)
	require.NotEmpty(t, body.VerificationToken)
	require.Equal(t, "_kurze-url-challenge.links.verein.test", body.Records.TXT.Name)
	require.Equal(t, body.VerificationToken, body.Records.TXT.Value)
	require.Equal(t, "links.verein.test", body.Records.CNAME.Name)
	require.NotEmpty(t, body.Records.CNAME.Value)
}

func TestClaimDomainIsRefusedBelowAdmin(t *testing.T) {
	// A domain is the namespace a team's links live in, not content.
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

func TestClaimDomainRejectsAnApex(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "verein.test"})

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
}

func TestTwoTeamsMayClaimTheSameHostname(t *testing.T) {
	// The whole point of the partial index: a claim is not a reservation.
	f := newTenancyFixture(t)

	first := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "contested.verein.test"})
	require.Equal(t, http.StatusCreated, first.Code)

	second := f.do(t, f.otherAdmin, http.MethodPost,
		"/v1/teams/"+f.otherTeamID.String()+"/domains",
		map[string]string{"hostname": "contested.verein.test"})
	require.Equal(t, http.StatusCreated, second.Code, "body: %s", second.Body.String())

	require.NotEqual(t,
		decode[api.Domain](t, first).VerificationToken,
		decode[api.Domain](t, second).VerificationToken,
		"each claim needs its own token, or the first claimant could verify the second's")
}

func TestListDomainsHidesAnotherTeamsDomains(t *testing.T) {
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/teams/"+f.teamID.String()+"/domains", nil)

	require.Equal(t, http.StatusOK, rec.Code)
	for _, item := range decode[api.Page[api.Domain]](t, rec).Items {
		require.Equal(t, f.teamID, item.TeamID)
	}
}

func TestGetDomainReturnsTheRecordsToCreate(t *testing.T) {
	f := newTenancyFixture(t)
	claimed := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.members[authz.RoleViewer], http.MethodGet,
		"/v1/domains/"+claimed.ID.String(), nil)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decode[api.Domain](t, rec)
	require.Equal(t, claimed.ID, body.ID)
	require.Equal(t, claimed.Hostname, body.Hostname)
	require.Equal(t, claimed.VerificationToken, body.VerificationToken,
		"the token must still be readable on a later GET, not only on the create response")
}

func TestGetDomainIs404ForANonMember(t *testing.T) {
	f := newTenancyFixture(t)
	claimed := claimDomain(t, f, "links.verein.test")

	rec := f.do(t, f.stranger, http.MethodGet, "/v1/domains/"+claimed.ID.String(), nil)

	require.Equal(t, http.StatusNotFound, rec.Code)
}

// claimDomain claims a hostname for the fixture's own team as its admin.
func claimDomain(t *testing.T, f *tenancyFixture, hostname string) api.Domain {
	t.Helper()
	return claimDomainAs(t, f, f.members[authz.RoleAdmin], f.teamID, hostname)
}

func claimDomainAs(
	t *testing.T, f *tenancyFixture, as testUser, teamID uuid.UUID, hostname string,
) api.Domain {
	t.Helper()
	rec := f.do(t, as, http.MethodPost, "/v1/teams/"+teamID.String()+"/domains",
		map[string]string{"hostname": hostname})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	return decode[api.Domain](t, rec)
}

// verifiedDomain claims a hostname and marks it verified directly, because
// the HTTP path to verified needs DNS this test has no control over. Added
// now so task 9 (verify-domain) and task 10 (delete-domain) find it here
// rather than each inventing their own; this task's own tests do not call it.
//
//nolint:unused // see comment above
func verifiedDomain(t *testing.T, f *tenancyFixture, hostname string) api.Domain {
	t.Helper()
	claimed := claimDomain(t, f, hostname)
	_, err := f.pool.Exec(t.Context(),
		`update domain set verification_status = 'verified', verified_at = now() where id = $1`,
		claimed.ID)
	require.NoError(t, err)
	claimed.VerificationStatus = "verified"
	return claimed
}

// createLinkOn puts one link on a domain, so the delete guard has something
// to refuse over. Added now so task 10 (delete-domain) finds it here; this
// task's own tests do not call it.
//
//nolint:unused // see comment above
func createLinkOn(t *testing.T, f *tenancyFixture, domainID uuid.UUID) {
	t.Helper()
	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/links",
		map[string]any{"destination_url": "https://example.org/", "domain_id": domainID})
	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
}
