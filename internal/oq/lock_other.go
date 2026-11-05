//go:build !unix

package oq

func lockDir(string) func() { return func() {} }
