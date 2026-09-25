// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package httpserver

import (
	"net/http"
	"testing"
	"time"
)

func TestApplySetsRequestLifetimePolicyAndPreservesWriteTimeout(t *testing.T) {
	server := &http.Server{WriteTimeout: 7 * time.Second}
	Apply(server)
	if server.ReadHeaderTimeout != ReadHeaderTimeout || server.ReadTimeout != ReadTimeout || server.IdleTimeout != IdleTimeout {
		t.Fatalf("Apply() produced unexpected timeouts: %#v", server)
	}
	if server.WriteTimeout != 7*time.Second {
		t.Fatalf("WriteTimeout = %v, want preserved value", server.WriteTimeout)
	}
}
