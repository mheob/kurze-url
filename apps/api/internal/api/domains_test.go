package api_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/api/internal/api"
	"github.com/mheob/kurze-url/apps/api/internal/authz"
	"github.com/mheob/kurze-url/apps/api/internal/domainverify"
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
	require.Equal(t, f.deps.Config.DomainDNSTarget, body.Records.CNAME.Value,
		"the CNAME value is what a Verein pastes into their DNS zone; it must be the configured target")
}

func TestClaimDomainIsRefusedBelowAdmin(t *testing.T) {
	// A domain is the namespace a team's links live in, not content.
	f := newTenancyFixture(t)

	rec := f.do(t, f.members[authz.RoleEditor], http.MethodPost,
		"/v1/teams/"+f.teamID.String()+"/domains",
		map[string]string{"hostname": "links.verein.test"})

	require.Equal(t, http.StatusForbidden, rec.Code)
}

// TestClaimDomainRejectsInvalidHostnames covers all three of
// NormalizeHostname's failure modes at the HTTP layer, not just the apex
// case: each must produce a 422 whose body actually carries that error's own
// wording, since ErrReserved and ErrMalformed share the same 422 line as
// ErrApex and a wrong mapping would still pass a test that checked only the
// status code.
func TestClaimDomainRejectsInvalidHostnames(t *testing.T) {
	f := newTenancyFixture(t)

	// The fixture's own sharedHostname ("shared-<suffix>.test") has only two
	// labels, so it trips ErrApex before NormalizeHostname ever reaches the
	// reserved check — that would test the wrong branch. A three-label
	// hostname that still equals the instance's own SharedDomainHostname is
	// what actually exercises ErrReserved, so it is set here instead of relied
	// on from the fixture.
	f.deps.Config.SharedDomainHostname = "reserved.verein.test"
	f.rebuildRouter()

	for _, tc := range []struct {
		name     string
		hostname string
		wantErr  error
	}{
		{"apex", "verein.test", domainverify.ErrApex},
		{"reserved (the instance's own shared hostname)", "reserved.verein.test", domainverify.ErrReserved},
		{"malformed", "https://x.verein.test/p", domainverify.ErrMalformed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.do(t, f.members[authz.RoleAdmin], http.MethodPost,
				"/v1/teams/"+f.teamID.String()+"/domains",
				map[string]string{"hostname": tc.hostname})

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, "body: %s", rec.Body.String())
			require.Contains(t, rec.Body.String(), tc.wantErr.Error())
		})
	}
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
	claimDomainAs(t, f, f.otherAdmin, f.otherTeamID, "hidden.verein.test")

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
	require.Equal(t, "_kurze-url-challenge.links.verein.test", body.Records.TXT.Name)
	require.Equal(t, body.VerificationToken, body.Records.TXT.Value)
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
