// This file is part of dummy-monitor.
//
// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package args

import (
	"fmt"
	"os"
)

// Tag is the current git tag
var Tag = "snapshot"

// Timestamp is the current timestamp
var Timestamp = "unknown"

// Parse arguments passed by the user
func Parse() {
	for _, arg := range os.Args[1:] {
		if arg == "" {
			continue
		}
		if arg == "-v" || arg == "--version" {
			fmt.Printf("dummy-monitor %s (build timestamp: %s)\n", Tag, Timestamp)
			os.Exit(0)
		}
		fmt.Fprintf(os.Stderr, "invalid argument: %s\n", arg)
		os.Exit(1)
	}
}
