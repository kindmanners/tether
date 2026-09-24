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

// Command pairing-test is a small manual smoke check for pairing-code display.
// The certificate-bound proof protocol is covered by internal/pairing tests.
package main

import (
	"fmt"

	"tether/internal/pairing"
)

func main() {
	code, err := pairing.Generate()
	if err != nil {
		panic(err)
	}
	fmt.Printf("Generated a %d-character pairing code that expires at %s.\n", len(code.String()), code.ExpiresAt().Format("15:04:05"))
	fmt.Println("Run `go test ./internal/pairing` to verify certificate-bound proof and replay handling.")
}
