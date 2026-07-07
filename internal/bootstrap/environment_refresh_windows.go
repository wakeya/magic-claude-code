//go:build windows

package bootstrap

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	hwndBroadcast       = 0xffff
	wmSettingChange     = 0x001a
	smtoAbortIfHung     = 0x0002
	environmentTimeoutM = 5000
)

var sendMessageTimeoutW = windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")

// broadcastEnvironmentChangeOS asks desktop shell processes to reload the
// persisted user environment. Already-running clients still need a restart.
func broadcastEnvironmentChangeOS() error {
	environment, err := windows.UTF16PtrFromString("Environment")
	if err != nil {
		return fmt.Errorf("encode Environment: %w", err)
	}

	var messageResult uintptr
	result, _, callErr := sendMessageTimeoutW.Call(
		uintptr(hwndBroadcast),
		uintptr(wmSettingChange),
		0,
		uintptr(unsafe.Pointer(environment)),
		uintptr(smtoAbortIfHung),
		uintptr(environmentTimeoutM),
		uintptr(unsafe.Pointer(&messageResult)),
	)
	if result != 0 {
		return nil
	}
	if callErr != nil && !errors.Is(callErr, windows.ERROR_SUCCESS) {
		return fmt.Errorf("SendMessageTimeoutW: %w", callErr)
	}
	return errors.New("SendMessageTimeoutW returned zero")
}
