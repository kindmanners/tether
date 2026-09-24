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

package pairing

import "testing"

func TestAllowAttemptRateLimitsOneSource(t *testing.T) {
	server := &Server{attempts: make(map[string]pairAttempts)}
	for range maxPairAttempts {
		if !server.allowAttempt("100.64.0.1:7420") {
			t.Fatal("allowAttempt rejected a retry inside the configured limit")
		}
	}
	if server.allowAttempt("100.64.0.1:7420") {
		t.Fatal("allowAttempt accepted a request beyond the configured limit")
	}
	if !server.allowAttempt("100.64.0.2:7420") {
		t.Fatal("allowAttempt rate-limited a different source")
	}
}
