package api

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
)

// Problem is a user-facing explanation of a failed request.
type Problem struct {
	// Summary says what went wrong in plain language.
	Summary string
	// Hint says what to do about it ("" when there is nothing specific).
	Hint string
}

// Explain turns a client error into a short explanation and a suggested fix.
// Unknown errors are returned verbatim with no hint.
func Explain(err error) Problem {
	if err == nil {
		return Problem{}
	}
	var e *Error
	if errors.As(err, &e) {
		return explainStatus(e)
	}

	msg := err.Error()
	var dnsErr *net.DNSError
	var certErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	var invalidErr x509.CertificateInvalidError
	switch {
	case strings.Contains(msg, "request timed out"):
		return Problem{"n8n did not respond in time",
			"Check that the instance is reachable from this machine, or raise `timeout` in the config."}
	case errors.As(err, &dnsErr):
		return Problem{fmt.Sprintf("Host %q could not be found", dnsErr.Name),
			"Check the instance `url` for typos."}
	case errors.Is(err, syscall.ECONNREFUSED) || strings.Contains(msg, "connection refused"):
		return Problem{"Connection refused: nothing is listening at this address",
			"Is n8n running? Check the instance `url` and port."}
	case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &invalidErr),
		strings.Contains(msg, "x509:"), strings.Contains(msg, "certificate"):
		return Problem{"The server's TLS certificate is not trusted",
			"Fix the certificate, or for a local/dev instance set `insecure_skip_verify: true`."}
	case strings.Contains(msg, "server gave HTTP response to HTTPS client"):
		return Problem{"The server speaks plain HTTP, not HTTPS",
			"Change the instance `url` to start with http://."}
	case strings.Contains(msg, "no such host"):
		return Problem{"Host could not be found", "Check the instance `url` for typos."}
	case strings.Contains(msg, "decoding response"):
		return Problem{"The server's reply was not n8n API data",
			"Make sure `url` points at n8n itself (e.g. https://n8n.example.com), not another site or a login page."}
	}
	return Problem{Summary: msg}
}

func explainStatus(e *Error) Problem {
	detail := ""
	if e.Message != "" {
		detail = " (" + e.Message + ")"
	}
	switch {
	case e.StatusCode == http.StatusUnauthorized:
		return Problem{"n8n rejected the API key",
			"Create a key in n8n under Settings → n8n API and put it in `api_key` (check the environment variable is exported)."}
	case e.StatusCode == http.StatusForbidden:
		return Problem{"The API key is not allowed to do this" + detail,
			"Use a key with the needed scopes, or a key from an owner/admin account. Some features also need a paid n8n licence."}
	case e.StatusCode == http.StatusNotFound && isAPIRoot(e.Path):
		return Problem{"The n8n public API was not found at this URL",
			"Use the instance's root URL (e.g. https://n8n.example.com, without /api/v1) and make sure the public API is enabled."}
	case e.StatusCode == http.StatusNotFound:
		return Problem{"Not found" + detail, "It may have been deleted. Press r to refresh."}
	case e.StatusCode == http.StatusTooManyRequests:
		return Problem{"n8n is rate limiting requests", "Wait a moment, or raise `refresh_interval` in the config."}
	case e.StatusCode >= 500:
		return Problem{fmt.Sprintf("n8n returned a server error (%d)%s", e.StatusCode, detail),
			"Check the n8n server logs. If n8n sits behind a proxy, check that the proxy can reach it."}
	}
	return Problem{Summary: e.Error()}
}

// isAPIRoot reports whether a 404 on path means the API itself is missing
// rather than a single resource (only list endpoints are used to probe).
func isAPIRoot(path string) bool {
	return path == "/workflows" || path == "/executions"
}
