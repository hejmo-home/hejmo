//go:build unix

package server

import "syscall"

// freeDiskBytes is the space left on the volume that holds dir.
func freeDiskBytes(dir string) (int64, bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, false
	}
	return int64(st.Bavail) * int64(st.Bsize), true
}
