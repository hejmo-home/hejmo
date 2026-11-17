//go:build !unix

package server

// freeDiskBytes is not known on this system.
func freeDiskBytes(string) (int64, bool) { return 0, false }
