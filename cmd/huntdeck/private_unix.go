//go:build unix

package main

import "syscall"

// privateFiles has the files HuntDeck creates (the database and its WAL,
// backups, the login browser's profile) readable by its own user only:
// they hold private programs' data and live sessions.
func privateFiles() { syscall.Umask(0o077) }
