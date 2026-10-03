// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

import "context"

// No native window outside Windows yet: the caller opens the browser instead.
func runWindow(context.Context, string, string) bool { return false }

// No native folder picker: the settings page falls back to a text field.
var pickDir func(string) (string, error)
