// Package goversion centralizes Go version requirements for gopernicus.
// To bump the minimum version: update MinGoVersion, run go mod edit -go=X.Y
// in both gopernicus/ and gopernicus-cli/, and update go.work.
package goversion

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// MinGoVersion is the minimum Go version required for gopernicus projects.
// Bootstrapped go.mod files are pinned to this version.
const MinGoVersion = "1.26"

// Check returns an error if the running Go toolchain is older than MinGoVersion.
func Check() error {
	running := runtime.Version() // e.g. "go1.26.0"
	running = strings.TrimPrefix(running, "go")
	if !MeetsMinimum(running, MinGoVersion) {
		return fmt.Errorf(
			"gopernicus requires Go %s or later (you have go%s)\n\nUpgrade at https://go.dev/dl/",
			MinGoVersion, running,
		)
	}
	return nil
}

// MeetsMinimum reports whether version v is >= min (both in "X.Y" or "X.Y.Z" form).
func MeetsMinimum(v, min string) bool {
	vParts := parseSemver(v)
	mParts := parseSemver(min)
	for i := 0; i < len(mParts) && i < len(vParts); i++ {
		if vParts[i] > mParts[i] {
			return true
		}
		if vParts[i] < mParts[i] {
			return false
		}
	}
	return len(vParts) >= len(mParts)
}

func parseSemver(v string) []int {
	parts := strings.Split(strings.Split(v, "-")[0], ".")
	nums := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			break
		}
		nums = append(nums, n)
	}
	return nums
}
