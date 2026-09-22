// Package vcs reports the binary's build version, stamped by the toolchain
// from VCS metadata at link time (debug.ReadBuildInfo) — never hardcoded, so
// the stamp cannot drift from the commit it was built from.
package vcs

import "runtime/debug"

func Version() string {
	bi, ok := debug.ReadBuildInfo()
	if ok {
		return bi.Main.Version
	}

	return ""
}
