//go:build !windows

package warp

import "fmt"

func defaultLocalUserStoragePath() (string, error) {
	return "", fmt.Errorf("warp local secure storage import is only supported on Windows")
}

func decryptLocalUserStorage(_ []byte) (string, error) {
	return "", fmt.Errorf("warp local secure storage import is only supported on Windows")
}
