//go:build !windows

package main

func acquireSingleInstance(name string) bool { return true }
