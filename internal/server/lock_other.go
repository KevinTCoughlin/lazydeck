//go:build !unix

package server

import "os"

// lockExclusiveNonblocking is a no-op on non-Unix platforms. lazydeck ships
// only macOS and Linux release binaries (see .goreleaser.yml), where
// lock_unix.go's flock-based implementation applies; this keeps the package
// compilable elsewhere without providing real single-instance protection.
func lockExclusiveNonblocking(f *os.File) error {
	return nil
}
