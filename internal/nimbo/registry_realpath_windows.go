//go:build windows

package nimbo

import (
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	registryKernel32      = syscall.NewLazyDLL("kernel32.dll")
	registryFinalPathProc = registryKernel32.NewProc("GetFinalPathNameByHandleW")
)

// registryReal resolves every link in an existing path. Since Go 1.23 filepath.EvalSymlinks no longer follows
// junctions (mount points), which need no special rights to create, so the final path is also asked of Windows itself.
func registryReal(p string) (string, error) {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	u, err := syscall.UTF16PtrFromString(real)
	if err != nil {
		return real, nil
	}
	const backupSemantics = 0x02000000 // lets a folder be opened
	h, err := syscall.CreateFile(u, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, backupSemantics, 0)
	if err != nil {
		return real, nil // cannot open: the caller's own checks (stat) will say so
	}
	defer syscall.CloseHandle(h)
	buf := make([]uint16, 1024)
	for {
		n, _, _ := registryFinalPathProc.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0)
		if n == 0 {
			return real, nil
		}
		if int(n) > len(buf) {
			buf = make([]uint16, n+1)
			continue
		}
		out := syscall.UTF16ToString(buf[:n])
		switch {
		case strings.HasPrefix(out, `\\?\UNC\`):
			out = `\\` + out[len(`\\?\UNC\`):]
		case strings.HasPrefix(out, `\\?\`):
			out = out[len(`\\?\`):]
		}
		return filepath.Clean(out), nil
	}
}
