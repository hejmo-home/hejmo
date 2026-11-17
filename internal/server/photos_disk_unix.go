//go:build unix

package server

import (
	"math"
	"syscall"
)

// freeDiskBytes is the space left on the volume that holds dir.
func freeDiskBytes(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	// Multiply unsigned and cap at the largest int64: the types differ per platform.
	avail, size := uint64(st.Bavail), uint64(st.Bsize) // #nosec G115 -- block counts and sizes are never negative
	if size != 0 && avail > math.MaxInt64/size {
		return math.MaxInt64, true
	}
	return int64(avail * size), true // #nosec G115 -- capped above
}
