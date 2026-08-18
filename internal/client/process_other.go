//go:build !unix

package client

import (
	"os/exec"
	"time"
)

// configureCancellation is a best-effort fallback for non-Unix platforms.
// lazydeck ships only macOS and Linux release binaries (see .goreleaser.yml),
// where the process-group implementation in process_unix.go applies; this
// keeps the package compilable elsewhere by at least bounding the wait after
// cancellation. exec.CommandContext still SIGKILLs the direct child.
func configureCancellation(cmd *exec.Cmd) {
	cmd.WaitDelay = 10 * time.Second
}
