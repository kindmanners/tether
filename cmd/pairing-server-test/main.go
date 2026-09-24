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

// Command pairing-server-test points developers to the automated pairing
// protocol tests. The previous harness sent the pairing code over an
// unauthenticated bootstrap connection, which is intentionally no longer a
// valid protocol flow.
package main

import "fmt"

func main() {
	fmt.Println("Run `go test ./internal/pairing` to exercise the certificate-bound pairing protocol.")
}
