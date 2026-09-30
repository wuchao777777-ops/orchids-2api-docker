//go:build linux

package selfupdate

import (
	"context"
	"debug/elf"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"time"
)

var servicePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]+\.service$`)

func platformCapability(executable, service string) string {
	if os.Getenv("ORCHIDS_UPDATE_ENABLED") != "true" {
		return "在线升级未启用"
	}
	if os.Geteuid() != 0 {
		return "在线升级需要 systemd root 服务"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "Docker 请更新镜像后重建容器"
	}
	if !servicePattern.MatchString(service) || os.Getenv("INVOCATION_ID") == "" {
		return "需要指定当前 systemd 服务"
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		return "systemd-run 不可用"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, "systemctl", "show", service, "-p", "ExecStart", "-p", "Restart", "-p", "MainPID").Output()
	text := string(output)
	if err != nil || !strings.Contains(text, "path="+executable+" ;") || !strings.Contains(text, fmt.Sprintf("MainPID=%d\n", os.Getpid())) || (!strings.Contains(text, "Restart=always") && !strings.Contains(text, "Restart=on-failure")) {
		return "systemd 服务与当前进程或重启策略不匹配"
	}
	if f, err := os.CreateTemp(filepath.Dir(executable), ".update-permission-*"); err != nil {
		return "可执行文件目录不可写"
	} else {
		f.Close()
		os.Remove(f.Name())
	}
	return ""
}
func fileLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("系统升级操作正在进行")
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
func validateBinary(path string) error {
	f, err := elf.Open(path)
	if err != nil {
		return errors.New("产物不是有效的 ELF 二进制")
	}
	defer f.Close()
	want := map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64}[runtime.GOARCH]
	if f.Class != elf.ELFCLASS64 || f.Machine != want || (f.Type != elf.ET_EXEC && f.Type != elf.ET_DYN) {
		return errors.New("产物架构不匹配")
	}
	return nil
}
func launchWatchdog(backup, state, service, executable, health, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// A transient unit survives stopping the application unit and does not inherit its cgroup.
	output, err := exec.CommandContext(ctx, "systemd-run", "--quiet", "--collect", "--unit=orchids-update-"+id, "--property=Type=exec", backup, "--upgrade-watchdog", state, service, executable, health, id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("无法启动独立回退守护进程：%w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
func restartService(service string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "systemctl", "reset-failed", service).Run()
	return exec.CommandContext(ctx, "systemctl", "restart", service).Run()
}
func runningHash(service string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", "show", service, "-p", "MainPID", "--value").Output()
	if err != nil {
		return "", err
	}
	pid := strings.TrimSpace(string(out))
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(pid) {
		return "", errors.New("service is not running")
	}
	return hashFile("/proc/" + pid + "/exe")
}
