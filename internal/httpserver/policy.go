// Package httpserver provides the conservative request-lifetime policy shared
// by Tether's control-plane servers. Streaming inference deliberately leaves
// WriteTimeout unset; the other servers return small JSON responses.
package httpserver

import (
	"net/http"
	"time"
)

const (
	ReadHeaderTimeout = 5 * time.Second
	ReadTimeout       = 30 * time.Second
	IdleTimeout       = 60 * time.Second
)

// Apply configures bounds that prevent incomplete or idle requests from
// holding a server connection indefinitely. Callers may set WriteTimeout when
// their response type is non-streaming.
func Apply(server *http.Server) {
	server.ReadHeaderTimeout = ReadHeaderTimeout
	server.ReadTimeout = ReadTimeout
	server.IdleTimeout = IdleTimeout
}
