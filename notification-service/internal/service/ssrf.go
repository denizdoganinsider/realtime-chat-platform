package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrForbiddenDestination is a webhook whose host resolved to somewhere this
// service must not reach into.
var ErrForbiddenDestination = errors.New("webhook destination is not allowed")

// forbiddenIP is the one rule for "may notification-service open a connection
// to this address". It is applied twice: to IP literals at registration, and to
// every address a hostname resolves to at dial time. The second is the one that
// matters - a hostname is validated as a string once, but what it resolves to
// is decided by whoever controls its DNS, at every delivery.
func forbiddenIP(ip net.IP, allowLoopback bool) bool {
	if ip.IsLoopback() {
		return !allowLoopback
	}
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || ip.IsInterfaceLocalMulticast()
}

// newSafeTransport is an http.Transport whose dialer resolves the host itself
// and refuses to connect to any forbidden address. Resolving here rather than
// in validation closes the DNS hole: a name that pointed somewhere public when
// it was registered, and at 10.0.0.5 when the delivery goes out, is stopped at
// the socket.
func newSafeTransport(allowLoopback bool) *http.Transport {
	dialer := &net.Dialer{Timeout: 5 * time.Second}

	return &http.Transport{
		MaxIdleConnsPerHost: 4,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}

			addrs, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, err
			}

			var lastErr error = fmt.Errorf("%w: %s", ErrForbiddenDestination, host)
			for _, addr := range addrs {
				if forbiddenIP(addr.IP, allowLoopback) {
					continue
				}
				conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(addr.IP.String(), port))
				if err == nil {
					return conn, nil
				}
				lastErr = err
			}

			return nil, lastErr
		},
	}
}
