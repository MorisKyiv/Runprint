//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package capture

import (
	"os"
	"os/exec"
	"syscall"
)

func handledSignals() []os.Signal {
	return []os.Signal{syscall.SIGHUP, os.Interrupt, syscall.SIGQUIT, syscall.SIGTERM}
}

func prepareCommand(cmd *exec.Cmd, isolatedProcessGroup bool) {
	if isolatedProcessGroup {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
}

func forwardSignal(cmd *exec.Cmd, sig os.Signal, isolatedProcessGroup bool) error {
	unixSignal, ok := sig.(syscall.Signal)
	if !ok || !isolatedProcessGroup {
		return cmd.Process.Signal(sig)
	}
	if err := syscall.Kill(-cmd.Process.Pid, unixSignal); err != nil {
		return cmd.Process.Signal(sig)
	}
	return nil
}

func forceKill(cmd *exec.Cmd, isolatedProcessGroup bool) error {
	if !isolatedProcessGroup {
		return cmd.Process.Kill()
	}
	groupErr := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	processErr := cmd.Process.Kill()
	if groupErr == nil || processErr == nil {
		return nil
	}
	return groupErr
}

func signalExitCode(sig os.Signal) (int, bool) {
	unixSignal, ok := sig.(syscall.Signal)
	if !ok || unixSignal <= 0 || unixSignal > 127 {
		return 0, false
	}
	return 128 + int(unixSignal), true
}

func portableSignalName(sig os.Signal) string {
	unixSignal, ok := sig.(syscall.Signal)
	if !ok {
		return "signal"
	}
	switch unixSignal {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGTERM:
		return "SIGTERM"
	default:
		return "signal"
	}
}

func processExitCode(state *os.ProcessState) (int, bool) {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return state.ExitCode(), state.ExitCode() >= 0
	}
	if status.Signaled() {
		return 128 + int(status.Signal()), true
	}
	return status.ExitStatus(), true
}
