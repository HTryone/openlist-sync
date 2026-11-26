package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// 暂存文件应还原到 LocalDir 下的原路径，并一起交给回收站
func TestStageAndRecycleRestoresOriginalPath(t *testing.T) {
	old := recycleFiles
	defer func() { recycleFiles = old }()
	var got []string
	recycleFiles = func(p []string) error { got = append(got, p...); return nil }

	root := t.TempDir()
	stage, local := filepath.Join(root, "stage"), filepath.Join(root, "L")
	writeFile(t, filepath.Join(stage, "sub", "a.txt"), "data")
	writeFile(t, filepath.Join(local, "keep.txt"), "k")

	rec, kept, errs := stageAndRecycle(stage, local, filepath.Join(root, "trash"))
	if rec != 1 || kept != 0 || len(errs) != 0 {
		t.Fatalf("rec=%d kept=%d errs=%v", rec, kept, errs)
	}
	want := filepath.Join(local, "sub", "a.txt")
	if len(got) != 1 || got[0] != want {
		t.Fatalf("回收站收到的路径 = %v，期望 [%s]", got, want)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatal("暂存目录应已清理")
	}
}

// 原路径已有同名文件时，不覆盖，加 (recovered N)
func TestStageAndRecycleCollisionSuffix(t *testing.T) {
	old := recycleFiles
	defer func() { recycleFiles = old }()
	var got []string
	recycleFiles = func(p []string) error { got = append(got, p...); return nil }

	root := t.TempDir()
	stage, local := filepath.Join(root, "stage"), filepath.Join(root, "L")
	writeFile(t, filepath.Join(stage, "a.txt"), "from cloud")
	writeFile(t, filepath.Join(local, "a.txt"), "user made this")

	rec, _, errs := stageAndRecycle(stage, local, filepath.Join(root, "trash"))
	if rec != 1 || len(errs) != 0 {
		t.Fatalf("rec=%d errs=%v", rec, errs)
	}
	if !strings.Contains(got[0], "(recovered 1)") {
		t.Fatalf("期望加 (recovered 1) 后缀，实际 %s", got[0])
	}
	if b, _ := os.ReadFile(filepath.Join(local, "a.txt")); string(b) != "user made this" {
		t.Fatal("原文件不应被覆盖")
	}
}

// 回收站不可用时，文件必须留档，不能被静默删除
func TestStageAndRecycleFallsBackWhenNoRecycleBin(t *testing.T) {
	old := recycleFiles
	defer func() { recycleFiles = old }()
	recycleFiles = func(p []string) error { return os.ErrPermission }

	root := t.TempDir()
	stage, local, trash := filepath.Join(root, "stage"), filepath.Join(root, "L"), filepath.Join(root, "trash")
	writeFile(t, filepath.Join(stage, "sub", "a.txt"), "data")

	rec, kept, errs := stageAndRecycle(stage, local, trash)
	if rec != 0 || kept != 1 || len(errs) == 0 {
		t.Fatalf("rec=%d kept=%d errs=%v", rec, kept, errs)
	}
	if _, err := os.Stat(filepath.Join(local, "sub", "a.txt")); !os.IsNotExist(err) {
		t.Fatal("回退后不应留在原路径（否则下次同步会把它传回去）")
	}
	found := false
	_ = filepath.WalkDir(trash, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "a.txt" {
			found = true
		}
		return nil
	})
	if !found {
		t.Fatal("回退后应在留档目录里找到 a.txt")
	}
}

// 暂存区必须落在 LocalDir 之外：LocalDir 与 recycle 目录（或有包含关系）时跳过
func TestNewStageDirOverlapGuard(t *testing.T) {
	a := &App{base: t.TempDir()}

	if got := a.newStageDir(Job{LocalDir: filepath.Join(a.base, "L")}); got == "" {
		t.Fatal("普通目录应返回暂存区路径")
	}
	if got := a.newStageDir(Job{LocalDir: a.base}); got != "" {
		t.Fatalf("LocalDir 包含程序目录时应跳过，实际 %q", got)
	}
	if got := a.newStageDir(Job{LocalDir: filepath.Join(a.base, "recycle", "x")}); got != "" {
		t.Fatalf("LocalDir 落在 recycle 下时应跳过，实际 %q", got)
	}
}

// 暂存区为空时什么都不做（正常同步没有远端删除）
func TestStageAndRecycleEmpty(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	rec, kept, errs := stageAndRecycle(stage, filepath.Join(root, "L"), filepath.Join(root, "trash"))
	if rec != 0 || kept != 0 || len(errs) != 0 {
		t.Fatalf("rec=%d kept=%d errs=%v", rec, kept, errs)
	}
}
