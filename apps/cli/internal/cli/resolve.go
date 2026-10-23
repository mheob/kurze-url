package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

// verifiedStatus is the verification_status of a domain whose owner has
// proved it. Only such a domain may carry links: the API refuses a link on any
// other, and saying so here, before the request, names the domain.
const verifiedStatus = "verified"

// resolveFolder turns the name a person typed into the ID of one of the
// team's folders.
//
// The match ignores case, because folder names are unique per team that way
// (the API's own rule) and "Newsletter" is not a different folder from
// "newsletter". An unknown name is an error that lists the folders there are,
// and never a new folder: a typo must not quietly create a second, empty
// folder that every later link then lands in.
//
// An empty name means "no folder" and costs no request.
func (a *App) resolveFolder(ctx context.Context, team apiclient.Membership, name string) (string, error) {
	if name == "" {
		return "", nil
	}
	folders, err := a.API.Folders(ctx, team.TeamID)
	if err != nil {
		return "", err
	}
	for _, f := range folders {
		if strings.EqualFold(f.Name, name) {
			return f.ID, nil
		}
	}

	names := make([]string, 0, len(folders))
	for _, f := range folders {
		names = append(names, f.Name)
	}
	// A sentence that ends "Available folders: ." reads as a defect, and a
	// team that has no folder yet is the likeliest person to hit this.
	available := a.Cat.T("link.noFolders")
	if len(names) > 0 {
		available = strings.Join(names, ", ")
	}
	return "", errors.New(a.Cat.T("link.folderUnknown", i18n.Args{
		"name":      name,
		"team":      team.Slug,
		"available": available,
	}))
}

// resolveDomain turns a hostname into the ID of one of the team's verified
// domains, or "" when the link belongs on the instance's shared host.
//
// The shared host is not one of the team's domains, so it is never looked up:
// the API puts a link there when it is sent no domain_id, and sending the
// request without one is what "use the shared host" means. The comparison
// ignores case because hostnames do, and runs against the environment's
// shared host, which a test or a staging instance overrides.
//
// A domain that exists but is not verified is told apart from one that does
// not exist, because the fix differs: finish verifying, or pick another
// name. A team that holds several rows for one hostname gets the verified one
// when any is.
//
// An empty host means "no domain" and costs no request.
func (a *App) resolveDomain(ctx context.Context, team apiclient.Membership, host string) (string, error) {
	if host == "" || strings.EqualFold(host, a.Env.SharedHost) {
		return "", nil
	}
	domains, err := a.API.Domains(ctx, team.TeamID)
	if err != nil {
		return "", err
	}

	known := false
	for _, d := range domains {
		if !strings.EqualFold(d.Hostname, host) {
			continue
		}
		if d.VerificationStatus == verifiedStatus {
			return d.ID, nil
		}
		known = true
	}

	args := i18n.Args{"host": host, "team": team.Slug, "shared": a.Env.SharedHost}
	if known {
		return "", errors.New(a.Cat.T("link.domainNotVerified", args))
	}
	return "", errors.New(a.Cat.T("link.domainUnknown", args))
}
