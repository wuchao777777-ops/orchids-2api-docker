//go:build windows

package warp

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func defaultLocalUserStoragePath() (string, error) {
	return defaultWindowsLocalUserStoragePath()
}

func decryptLocalUserStorage(encrypted []byte) (string, error) {
	if len(encrypted) == 0 {
		return "", nil
	}

	in := windows.DataBlob{
		Size: uint32(len(encrypted)),
		Data: &encrypted[0],
	}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))

	decrypted := unsafe.Slice(out.Data, out.Size)
	return string(decrypted), nil
}
