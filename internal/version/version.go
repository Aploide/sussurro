package version

// Version holds the current version of the application.
//
// It is a var, not a const, so release builds can stamp it from the git tag:
//
//	go build -ldflags "-X github.com/aploide/sussurro/internal/version.Version=2.4"
//
// The Makefile does this automatically when VERSION is set, and the release
// workflow sets VERSION from the pushed tag. Untagged local builds keep "dev".
var Version = "dev"
