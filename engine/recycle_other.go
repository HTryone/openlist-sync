//go:build !windows

package main

import "fmt"

func init() {
	recycleFiles = func(paths []string) error { return fmt.Errorf("当前系统无回收站") }
}
