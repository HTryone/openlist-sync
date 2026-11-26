//go:build windows

package main

import "testing"

// 无界面自检：COM 能创建 IFileOpenDialog 对象（不弹窗）
func TestComPickerSelfCheck(t *testing.T) {
	if err := comPickerSelfCheck(); err != nil {
		t.Fatalf("COM 选择器自检失败: %v", err)
	}
}
