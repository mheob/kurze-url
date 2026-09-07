package domainverify

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// This file is package domainverify, not domainverify_test: it exercises
// refuseNonPublicAddress directly, which is deliberately unexported — it is
// not part of this package's contract, only a mechanism NewVerifier wires
// into its dialer. Testing it here, rather than only through the wiring
// test in verify_test.go, is what makes deleting the check itself a failing
// test rather than a hang or an unrelated dial error.
func TestRefuseNonPublicAddress(t *testing.T) {
	for name, address := range map[string]string{
		"loopback":   "127.0.0.1:443",
		"link-local": "169.254.169.254:80",
		"private":    "10.0.0.1:443",
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, refuseNonPublicAddress("tcp", address))
		})
	}

	t.Run("public address", func(t *testing.T) {
		require.NoError(t, refuseNonPublicAddress("tcp", "93.184.216.34:443"))
	})

	t.Run("unparseable address", func(t *testing.T) {
		require.Error(t, refuseNonPublicAddress("tcp", "not-an-address"))
	})
}
