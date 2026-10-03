// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web embeds the mobile client and the local admin page.
package web

import "embed"

//go:embed index.html app.js admin.html icon.svg icon.png
var FS embed.FS
