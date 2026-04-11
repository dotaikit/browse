package browser

import (
	"fmt"
	"net/url"
)

// ValidateProxy validates a proxy URL string.
// Empty string is valid (no proxy). Otherwise the URL must parse correctly,
// have a scheme in {http, https, socks5}, and a non-empty host.
func ValidateProxy(raw string) error {
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid proxy URL %q: %w", raw, err)
	}

	switch u.Scheme {
	case "http", "https", "socks5":
		// ok
	default:
		return fmt.Errorf("invalid proxy URL %q: scheme must be http, https, or socks5", raw)
	}

	if u.Host == "" {
		return fmt.Errorf("invalid proxy URL %q: missing host", raw)
	}

	return nil
}
