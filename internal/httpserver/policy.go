// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as
// published by the Free Software Foundation, either version 3 of the
// License, or (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

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
