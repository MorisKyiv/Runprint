//go:build windows || plan9 || js || wasip1

package main

func suppressBrokenPipe() func() {
	return func() {}
}
