package main

import (
	"strings"
)

// GitignoreToRclone 把 .nosy（gitignore 语法）翻译成 rclone --filter-from 规则。
//
// 两个关键语义差异：
//  1. gitignore: 含 "/" 的模式锚定到该文件所在目录（即同步根）；不含 "/" 的匹配任意层级。
//     rclone: 不含前导 "/" 的模式匹配任意层级；含前导 "/" 才锚定根。
//     => 含 "/" 的 gitignore 模式要加前导 "/"；不含的加 "**/"。
//  2. gitignore: 后匹配覆盖先匹配（last-match-wins）；rclone: 先匹配生效（first-match-wins）。
//     => 先输出所有 "!" 包含规则，再输出所有排除规则。
//
// 另：gitignore 里 "目录/" 与 "dir/*" 都表示递归排除其下所有内容，翻译为 "<dir>/**"。
func GitignoreToRclone(content string) []string {
	var inc, exc []string
	for _, raw := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		neg := false
		if strings.HasPrefix(line, "!") {
			neg = true
			line = strings.TrimSpace(strings.TrimPrefix(line, "!"))
		}
		// gitignore 允许 \# \! 转义，简单处理
		line = strings.TrimPrefix(line, "\\")
		if line == "" {
			continue
		}
		glob := convertPattern(line)
		if glob == "" {
			continue
		}
		if neg {
			inc = append(inc, "+ "+glob)
		} else {
			exc = append(exc, "- "+glob)
		}
	}
	out := make([]string, 0, len(inc)+len(exc))
	out = append(out, inc...)
	out = append(out, exc...)
	return out
}

// convertPattern 把单条 gitignore 模式转成 rclone 模式。
// rclone 语义：含前导 "/" 才锚定到同步根；不含则匹配任意层级。
// 所以：gitignore 里锚定的（显式 "/" 或含内部 "/"）加前导 "/"；其余保持不加前缀（天然任意层级）。
func convertPattern(p string) string {
	isDir := strings.HasSuffix(p, "/")
	anchor := strings.HasPrefix(p, "/")
	core := strings.Trim(p, "/")
	if core == "" {
		return ""
	}
	if !anchor && strings.Contains(core, "/") {
		anchor = true // gitignore：含内部 "/" 即锚定根
	}
	glob := core
	if isDir {
		glob = core + "/**"
	} else if strings.HasSuffix(glob, "/*") {
		glob = strings.TrimSuffix(glob, "/*") + "/**"
	}
	if anchor && !strings.HasPrefix(glob, "/") {
		glob = "/" + glob
	}
	return glob
}

// FiltersFileContent 生成 --filter-from 文件正文（含头注释与自动排除 .nosy 自身）
func FiltersFileContent(nosy string) string {
	var b strings.Builder
	b.WriteString("# 由面板根据 .nosy 自动生成，请勿手改；改 .nosy 即可\n")
	b.WriteString("- /.nosy\n") // 过滤规则文件本身不参与同步
	for _, r := range GitignoreToRclone(nosy) {
		b.WriteString(r + "\n")
	}
	return b.String()
}
