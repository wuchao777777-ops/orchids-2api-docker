//go:build !linux

package selfupdate

import "errors"

func platformCapability(string, string) string {
	return "当前平台仅支持检查更新；在线升级需要 Linux systemd"
}
func fileLock(string) (func(), error) { return nil, errors.New("online update unsupported") }
func validateBinary(string) error     { return errors.New("online update unsupported") }
func launchWatchdog(string, string, string, string, string, string) error {
	return errors.New("online update unsupported")
}
func restartService(string) error        { return errors.New("online update unsupported") }
func runningHash(string) (string, error) { return "", errors.New("online update unsupported") }
