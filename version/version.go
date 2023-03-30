// Package version provides information about the version of the operator.
//
// This file defines metadata injected into the binary at build time.
package version

var (
	// Version is the operator version
	Version = "0.0.1"
	// GitCommit is the current git commit hash
	GitCommit = "n/a"
	// BuildDate is the build date
	BuildDate = "n/a"
)
