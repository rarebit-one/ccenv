//go:build darwin

package main

import (
	"syscall"
	"unsafe"
)

type terminalState = syscall.Termios

func terminalIoctl(fd uintptr, request uintptr, state *terminalState) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(state)))
	if errno != 0 {
		return errno
	}
	return nil
}

func terminalIsTerminal(fd uintptr) bool {
	var state terminalState
	return terminalIoctl(fd, syscall.TIOCGETA, &state) == nil
}

func terminalMakeRaw(fd uintptr) (terminalState, error) {
	var old terminalState
	if err := terminalIoctl(fd, syscall.TIOCGETA, &old); err != nil {
		return old, err
	}
	raw := old
	raw.Iflag &^= syscall.BRKINT | syscall.ICRNL | syscall.INPCK | syscall.ISTRIP | syscall.IXON
	raw.Oflag &^= syscall.OPOST
	raw.Cflag &^= syscall.CSIZE | syscall.PARENB
	raw.Cflag |= syscall.CS8
	raw.Lflag &^= syscall.ECHO | syscall.ICANON | syscall.IEXTEN | syscall.ISIG
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0
	if err := terminalIoctl(fd, syscall.TIOCSETA, &raw); err != nil {
		return old, err
	}
	return old, nil
}

func terminalRestore(fd uintptr, old terminalState) error {
	return terminalIoctl(fd, syscall.TIOCSETA, &old)
}
