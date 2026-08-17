//go:build windows

package capture

import (
	"os"
	"os/exec"
)

func handledSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}

func prepareCommand(_ *exec.Cmd, _ bool) {}

func forwardSignal(cmd *exec.Cmd, _ os.Signal, _ bool) error {
	// Windows cannot deliver os.Interrupt to an arbitrary child process.
	return cmd.Process.Kill()
}

func forceKill(cmd *exec.Cmd, _ bool) error {
	return cmd.Process.Kill()
}

func signalExitCode(sig os.Signal) (int, bool) {
	if sig == os.Interrupt {
		return 130, true
	}
	return 0, false
}

func portableSignalName(_ os.Signal) string {
	return "interrupt"
}

func processExitCode(state *os.ProcessState) (int, bool) {
	code := state.ExitCode()
	return code, code >= 0
}
