// Package version identifies the release and source revision of each binary.
package version

// Build scripts inject the release and Git revision with -ldflags.
var Version = "0.12.0-dev"
var Revision = "local"

func String() string {
	return "STT-widget " + Version + " (" + Revision + ")"
}
