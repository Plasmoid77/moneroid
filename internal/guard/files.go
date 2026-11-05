package guard

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Plasmoid77/moneroid/internal/status"
)

// Lock takes the exclusive guard lock (GUARD-04). The guard creates the file;
// a CLI without it (no guard installed, no access) gets fs.ErrNotExist or a
// permission error and proceeds unlocked. Symlinks and non-regular files are refused.
func Lock(path string, create bool) (unlock func(), err error) {
	flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_NONBLOCK | syscall.O_CLOEXEC
	if create {
		flags |= syscall.O_CREAT
	}
	fd, err := syscall.Open(path, flags, 0o644)
	if err != nil {
		return func() {}, &fs.PathError{Op: "open", Path: path, Err: err}
	}
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFREG {
		syscall.Close(fd)
		return func() {}, fmt.Errorf("%s is not a regular file", path)
	}
	if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
		syscall.Close(fd)
		return func() {}, &fs.PathError{Op: "flock", Path: path, Err: err}
	}
	return func() { syscall.Flock(fd, syscall.LOCK_UN); syscall.Close(fd) }, nil
}

// WriteFile replaces path atomically through an exclusively created temporary
// file, so a name planted in a group-writable directory is never followed.
func WriteFile(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.WriteString(content)
	cerr := f.Close()
	if err := errors.Join(werr, cerr, os.Chmod(tmp, 0o644)); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// Manual wraps a user's start/stop/restart (CLI and TUI): under the lock it
// removes the hold marker when XMRig is among units, so the guard never undoes
// the user's decision. Call done after the systemctl operation finished.
func Manual(units []string, xmrigUnit string) (cleared bool, done func(), err error) {
	touches := false
	for _, u := range units {
		touches = touches || u == xmrigUnit
	}
	if !touches {
		return false, func() {}, nil
	}
	unlock, lerr := Lock(LockFile, false)
	if lerr != nil && !errors.Is(lerr, fs.ErrNotExist) {
		err = lerr // proceed unlocked, but say so
	}
	rerr := os.Remove(status.GuardHeldFile)
	switch {
	case rerr == nil:
		cleared = true
	case errors.Is(rerr, fs.ErrNotExist):
	default:
		err = errors.Join(err, rerr)
	}
	return cleared, unlock, err
}
