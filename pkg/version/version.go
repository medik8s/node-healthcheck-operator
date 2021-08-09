package version

import (
	"fmt"
	"runtime"
)

var (
	// goVersion is a constant representing the Go runtime version
	goVersion string = runtime.Version()
	// commitFromGit is a constant representing the source version that
	// generated this build. It should be set during build via -ldflags.
	commitFromGit string
	// versionFromGit is a constant representing the version tag that
	// generated this build. It should be set during build via -ldflags.
	versionFromGit = "unknown"
	// build date in ISO8601 format, output of $(date -u +'%Y-%m-%dT%H:%M:%SZ')
	buildDate string
	// state of git tree, either "clean" or "dirty"
	gitTreeState string
)

func String() string {
	return fmt.Sprintf("goVersion: %s, commitFromGit: %s, versoinFromGit: %s, buildDate: %s, gitTreeState: %s",
		goVersion, commitFromGit, versionFromGit, buildDate, gitTreeState)
}
