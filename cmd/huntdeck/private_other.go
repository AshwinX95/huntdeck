//go:build !unix

package main

// privateFiles does nothing where there's no umask; see private_unix.go.
func privateFiles() {}
