// 网盘删除 → 本地回收站：bisync 的 Path1 删除先落到暂存区（--backup-dir1），
// 本文件把它还原到原路径、再交给系统回收站（Windows）/ 留档（其他系统）。
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// recycleFiles 由平台实现：Windows 送回收站，其他平台返回错误（调用方回退到留档目录）。
var recycleFiles = func(paths []string) error { return fmt.Errorf("当前系统不支持回收站") }

// stageAndRecycle 处理 bisync --backup-dir1 暂存下来的文件：
//  1. 逐个还原到 LocalDir 下原路径（若已存在同名文件，加 (recovered N) 后缀）；
//  2. 一并送进系统回收站，让用户在资源管理器里能"还原"到原位置；
//  3. 回收站不可用（U 盘/FAT32/网络盘/组策略关闭/超过配额）时，改存到 fallbackDir，绝不静默删除。
//
// 返回：成功进回收站的数量、退回归档的数量、错误描述（供日志显示）。
func stageAndRecycle(stageDir, localDir, fallbackDir string) (recycled, kept int, errs []string) {
	// 只删空目录：万一有文件没搬走，留在暂存区里等用户处理，不做静默删除。
	defer func() {
		if stageEmpty(stageDir) {
			_ = os.RemoveAll(stageDir)
		}
	}()

	var files []string
	err := filepath.WalkDir(stageDir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 暂存区异常不阻断同步
		}
		if d.IsDir() {
			return nil
		}
		files = append(files, p)
		return nil
	})
	if err != nil || len(files) == 0 {
		return 0, 0, nil
	}

	type item struct{ stage, target, rel string }
	var restored []item
	for _, src := range files {
		rel, err := filepath.Rel(stageDir, src)
		if err != nil {
			errs = append(errs, fmt.Sprintf("跳过 %s: %v", src, err))
			continue
		}
		target := filepath.Join(localDir, rel)
		if _, statErr := os.Stat(target); statErr == nil {
			target = uniquePath(target)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			errs = append(errs, fmt.Sprintf("恢复 %s 失败: %v", rel, err))
			continue
		}
		if err := moveFile(src, target); err != nil {
			errs = append(errs, fmt.Sprintf("恢复 %s 失败: %v", rel, err))
			continue
		}
		restored = append(restored, item{src, target, rel})
	}
	if len(restored) == 0 {
		return 0, 0, errs
	}

	paths := make([]string, 0, len(restored))
	for _, it := range restored {
		paths = append(paths, it.target)
	}
	if err := recycleFiles(paths); err == nil {
		return len(paths), 0, errs
	} else {
		errs = append(errs, "回收站不可用（"+err.Error()+"），改为留档")
	}

	// 回退：能搬的搬去 fallbackDir，搬不动的就留在原位（宁可多留文件，也不删）。
	for _, it := range restored {
		if _, err := os.Stat(it.target); err != nil {
			continue
		}
		dst := filepath.Join(fallbackDir, timeStamp(), it.rel)
		if _, err := os.Stat(dst); err == nil {
			dst = uniquePath(dst)
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			errs = append(errs, fmt.Sprintf("留档 %s 失败: %v", it.rel, err))
			continue
		}
		if err := moveFile(it.target, dst); err != nil {
			errs = append(errs, fmt.Sprintf("留档 %s 失败: %v", it.rel, err))
			continue
		}
		kept++
	}
	return 0, kept, errs
}

// uniquePath 在文件名后追加 (recovered N)，避开同名文件。
func uniquePath(p string) string {
	dir, name := filepath.Split(p)
	ext := filepath.Ext(name)
	base := name[:len(name)-len(ext)]
	for i := 1; i < 1000; i++ {
		cand := filepath.Join(dir, fmt.Sprintf("%s (recovered %d)%s", base, i, ext))
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
	}
	return filepath.Join(dir, fmt.Sprintf("%s (recovered)%s", base, ext))
}

// moveFile 先试改名，跨卷失败再复制后删除。
func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}

func timeStamp() string { return time.Now().Format("20060102-150405") }

// stageEmpty 判断暂存区里是否已经没有文件（空目录可安全删除）。
func stageEmpty(dir string) bool {
	empty := true
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			empty = false
		}
		return nil
	})
	return empty
}
