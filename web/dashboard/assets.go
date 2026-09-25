// Copyright (C) 2026 kindmanners on github
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package dashboard exposes the browser dashboard assets embedded in the
// tether-dashboard binary.
package dashboard

import "embed"

// Assets contains the complete static dashboard application.
//
//go:embed index.html app.js styles.css
var Assets embed.FS
