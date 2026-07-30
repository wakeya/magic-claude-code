//go:build windows

package providerquota

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func applyScriptWorkerHardMemoryLimit(limit uint64) (func(), error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create worker job: %w", err)
	}
	cleanup := func() {
		_ = windows.CloseHandle(job)
	}

	var info windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
	info.ProcessMemoryLimit = uintptr(limit)
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		cleanup()
		return nil, fmt.Errorf("set worker job limit: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
		cleanup()
		return nil, fmt.Errorf("assign worker job: %w", err)
	}
	return cleanup, nil
}
