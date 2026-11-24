//go:build windows

package main

import (
	"strings"
	"testing"
)

// 验证多选文件夹对话框脚本能编译、COM 接口能初始化（无界面自检，不弹窗）
func TestPickerCompiles(t *testing.T) {
	out, err := runPicker("compilecheck")
	if err != nil {
		t.Fatalf("runPicker err: %v\nout=%s", err, out)
	}
	if !strings.Contains(out, "COMPILE:OK") {
		t.Fatalf("picker compile check failed:\n%s", out)
	}
}
