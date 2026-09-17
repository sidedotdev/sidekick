package server

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

type ownedControlListener struct {
	net.Listener
	path string
	info os.FileInfo
	lock *os.File
	once sync.Once
	err  error
}

func (l *ownedControlListener) Close() error {
	l.once.Do(func() {
		l.err = l.Listener.Close()
		current, err := os.Lstat(l.path)
		if err == nil && os.SameFile(l.info, current) {
			l.err = errors.Join(l.err, os.Remove(l.path))
		} else if err != nil && !os.IsNotExist(err) {
			l.err = errors.Join(l.err, err)
		}
		l.err = errors.Join(l.err, l.lock.Close())
	})
	return l.err
}

func listenControl(socket string) (net.Listener, error) {
	// The stable lock inode must survive shutdown so competing processes
	// cannot acquire locks on different files for the same socket.
	fd, err := unix.Open(socket+".lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	lock := os.NewFile(uintptr(fd), socket+".lock")
	success := false
	defer func() {
		if !success {
			_ = lock.Close()
		}
	}()
	info, err := lock.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("credential proxy lock must be a private regular file")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return nil, errors.New("credential proxy control socket is owned")
	}
	currentLock, err := os.Lstat(socket + ".lock")
	if err != nil || !os.SameFile(info, currentLock) {
		return nil, errors.New("credential proxy lock changed")
	}
	existing, err := os.Lstat(socket)
	if err == nil {
		if existing.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("credential proxy control path is not a socket")
		}
		conn, probeErr := net.DialTimeout("unix", socket, 250*time.Millisecond)
		if probeErr == nil {
			_ = conn.Close()
			return nil, errors.New("credential proxy control socket is live")
		}
		if !errors.Is(probeErr, unix.ECONNREFUSED) {
			return nil, errors.New("credential proxy control socket liveness is uncertain")
		}
		current, statErr := os.Lstat(socket)
		if statErr != nil || !os.SameFile(existing, current) {
			return nil, errors.New("credential proxy control socket changed")
		}
		if err := os.Remove(socket); err != nil {
			return nil, err
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	ownedInfo, err := os.Lstat(socket)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	owned := &ownedControlListener{Listener: listener, path: socket, info: ownedInfo, lock: lock}
	if err := os.Chmod(socket, 0600); err != nil {
		_ = owned.Close()
		return nil, err
	}
	success = true
	return owned, nil
}
