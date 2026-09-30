//go:build windows

package controller

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	stopEnvtestProcesses = killEnvtestChildren
}

// envtestExecutables are the processes envtest starts.
var envtestExecutables = map[string]bool{"etcd.exe": true, "kube-apiserver.exe": true}

// killEnvtestChildren kills the etcd and kube-apiserver processes the test
// binary started and waits for them to exit. envtest stops them with SIGTERM,
// which Windows cannot deliver, so they would otherwise outlive the run.
func killEnvtestChildren() error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return fmt.Errorf("list processes: %w", err)
	}
	defer func() { _ = windows.CloseHandle(snapshot) }()

	self := uint32(os.Getpid())
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	var errs []error
	next := windows.Process32First(snapshot, &entry)
	for ; next == nil; next = windows.Process32Next(snapshot, &entry) {
		name := windows.UTF16ToString(entry.ExeFile[:])
		if entry.ParentProcessID != self || !envtestExecutables[name] {
			continue
		}
		p, err := os.FindProcess(int(entry.ProcessID))
		if err != nil {
			errs = append(errs, fmt.Errorf("find %s (%d): %w", name, entry.ProcessID, err))
			continue
		}
		if err := p.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			errs = append(errs, fmt.Errorf("kill %s (%d): %w", name, entry.ProcessID, err))
			continue
		}
		_, _ = p.Wait()
	}
	if !errors.Is(next, windows.ERROR_NO_MORE_FILES) {
		errs = append(errs, fmt.Errorf("list processes: %w", next))
	}
	return errors.Join(errs...)
}
