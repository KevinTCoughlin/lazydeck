package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// latestReleaseURL is GitHub's "latest release" endpoint, which by design
// skips drafts and prereleases (release candidates and the rolling nightly).
const latestReleaseURL = "https://api.github.com/repos/kevintcoughlin/lazydeck/releases/latest"

// runVersionCheck implements `lazydeck version --check`: print the build line,
// then compare it against the latest stable release. It only touches the
// network when explicitly asked to; lazydeck never checks for updates on its
// own.
func runVersionCheck(w io.Writer) error {
	if _, err := fmt.Fprintln(w, versionString()); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg, err := checkForUpdate(ctx, http.DefaultClient, latestReleaseURL, effectiveVersion())
	if err != nil {
		return fmt.Errorf("checking for updates: %w", err)
	}
	_, err = fmt.Fprintln(w, msg)
	return err
}

// checkForUpdate fetches the latest release from url and describes how it
// relates to current.
func checkForUpdate(ctx context.Context, client *http.Client, url, current string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lazydeck/"+current)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&release); err != nil {
		return "", fmt.Errorf("decoding latest release: %w", err)
	}
	latest := strings.TrimPrefix(release.TagName, "v")
	if latest == "" {
		return "", fmt.Errorf("latest release has no tag")
	}

	cmp, ok := compareVersions(strings.TrimPrefix(current, "v"), latest)
	switch {
	case !ok:
		return fmt.Sprintf("Latest release is v%s (%s); this build (%s) can't be compared to it.", latest, release.HTMLURL, current), nil
	case cmp < 0:
		return fmt.Sprintf("lazydeck v%s is available (you have %s): %s", latest, current, release.HTMLURL), nil
	default:
		return fmt.Sprintf("lazydeck %s is up to date (latest release: v%s).", current, latest), nil
	}
}

// compareVersions orders two semantic versions (MAJOR.MINOR.PATCH with an
// optional -prerelease; build metadata is ignored), returning -1, 0, or 1.
// ok is false if either side isn't a semantic version, e.g. a "dev" build.
func compareVersions(a, b string) (cmp int, ok bool) {
	pa, ok := parseSemver(a)
	if !ok {
		return 0, false
	}
	pb, ok := parseSemver(b)
	if !ok {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if pa.core[i] != pb.core[i] {
			return sign(pa.core[i] - pb.core[i]), true
		}
	}
	// A version without a prerelease outranks the same version with one.
	switch {
	case pa.pre == nil && pb.pre == nil:
		return 0, true
	case pa.pre == nil:
		return 1, true
	case pb.pre == nil:
		return -1, true
	}
	for i := 0; i < len(pa.pre) && i < len(pb.pre); i++ {
		if c := comparePrereleaseIdent(pa.pre[i], pb.pre[i]); c != 0 {
			return c, true
		}
	}
	return sign(len(pa.pre) - len(pb.pre)), true
}

type semver struct {
	core [3]int
	pre  []string
}

func parseSemver(v string) (semver, bool) {
	var s semver
	v, _, _ = strings.Cut(v, "+")
	core, pre, hasPre := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return s, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return s, false
		}
		s.core[i] = n
	}
	if hasPre {
		if pre == "" {
			return s, false
		}
		s.pre = strings.Split(pre, ".")
	}
	return s, true
}

// comparePrereleaseIdent follows semver precedence: numeric identifiers
// compare numerically and sort before alphanumeric ones, which compare
// lexically.
func comparePrereleaseIdent(a, b string) int {
	na, aErr := strconv.Atoi(a)
	nb, bErr := strconv.Atoi(b)
	switch {
	case aErr == nil && bErr == nil:
		return sign(na - nb)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
