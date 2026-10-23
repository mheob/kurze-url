package auth

import (
	"errors"
	"fmt"
	"net"
	"strconv"
)

// ErrPortsBusy means none of the loopback ports the login can use was free.
// Supabase matches a redirect URI exactly, port included, so the CLI cannot
// fall back to any other port: it has to be one of the registered ones.
var ErrPortsBusy = errors.New("no free loopback port for the login callback")

// bindFirstFree listens on 127.0.0.1 at the first of ports that can be
// bound, and returns the listener with the port it actually got. A port of 0
// asks the system for any free one, which only tests use: the registered
// redirect URIs name three fixed ports.
//
// The address is the loopback one and never a wildcard, so the callback
// cannot be reached from the network.
func bindFirstFree(ports []int) (net.Listener, int, error) {
	var last error
	for _, port := range ports {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
		if err != nil {
			last = err
			continue
		}
		return ln, ln.Addr().(*net.TCPAddr).Port, nil
	}
	if last == nil {
		return nil, 0, fmt.Errorf("%w: no port was given", ErrPortsBusy)
	}
	return nil, 0, fmt.Errorf("%w (tried %v): %w", ErrPortsBusy, ports, last)
}
