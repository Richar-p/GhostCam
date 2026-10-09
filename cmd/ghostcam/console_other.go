// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build !windows

package main

func osLanguage() string { return "" } // the environment variables already say it
func prepareConsole()    {}
