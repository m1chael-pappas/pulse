//go:build !darwin

package main

func redirectStderr(string) (restore func(), err error) {
	return func() {}, nil
}
