//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package main

import (
	"os"
	"os/signal"
	"syscall"
)

func suppressBrokenPipe() func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGPIPE)
	return func() {
		signal.Stop(signals)
	}
}
