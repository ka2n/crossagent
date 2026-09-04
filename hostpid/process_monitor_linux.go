//go:build linux

package hostpid

import (
	"errors"
	"fmt"
	"sync"
	"syscall"
	"unsafe"
)

// pidfd_open is syscall 434 on the Linux ABIs supported by the standard
// syscall package. The standard library does not expose this constant on all
// Go versions, so use the stable kernel number while keeping the rest of the
// implementation dependency-free.
const pidfdOpenSyscall = 434

const pollIn int16 = 0x0001

type pollFD struct {
	fd      int32
	events  int16
	revents int16
}

func newProcessMonitor(r Resolver, identity ProcessIdentity, onExit func()) (ProcessMonitor, error) {
	hostPID, err := r.ResolveForMonitoring(identity)
	if err != nil {
		return nil, err
	}

	fd, err := openPIDFD(hostPID)
	if err != nil {
		return nil, fmt.Errorf("pidfd_open pid=%d: %w", hostPID, err)
	}

	monitor := &pidfdProcessMonitor{
		fd:     fd,
		done:   make(chan struct{}),
		onExit: onExit,
	}
	go monitor.wait()
	return monitor, nil
}

func openPIDFD(pid int) (int, error) {
	if pid <= 0 {
		return -1, fmt.Errorf("invalid pid %d", pid)
	}
	rawFD, _, errno := syscall.Syscall6(
		uintptr(pidfdOpenSyscall),
		uintptr(pid),
		0,
		0,
		0,
		0,
		0,
	)
	if errno != 0 {
		return -1, errno
	}
	return int(rawFD), nil
}

type pidfdProcessMonitor struct {
	fd     int
	done   chan struct{}
	onExit func()

	once sync.Once
}

func (m *pidfdProcessMonitor) Close() error {
	if m == nil {
		return nil
	}

	var err error
	m.once.Do(func() {
		close(m.done)
		err = syscall.Close(m.fd)
		if errors.Is(err, syscall.EBADF) {
			err = nil
		}
	})
	return err
}

func (m *pidfdProcessMonitor) wait() {
	err := pollPIDFD(m.fd)

	// Check done before considering either a poll event or an error. Closing
	// the fd wakes poll on some kernels with EBADF; an intentional close must
	// never turn into an onExit notification.
	select {
	case <-m.done:
		return
	default:
	}

	if err != nil || m.onExit == nil {
		return
	}
	m.onExit()
}

func pollPIDFD(fd int) error {
	pollFD := pollFD{fd: int32(fd), events: pollIn}
	for {
		result, _, errno := syscall.Syscall6(
			syscall.SYS_POLL,
			uintptr(unsafe.Pointer(&pollFD)),
			1,
			^uintptr(0), // timeout=-1 (the kernel argument is an int)
			0,
			0,
			0,
		)
		if errno == syscall.EINTR {
			continue
		}
		if errno != 0 {
			return errno
		}
		if result == 0 {
			return errors.New("pidfd poll unexpectedly timed out")
		}
		return nil
	}
}
