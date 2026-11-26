package main

import (
	"reflect"
	"strings"
	"testing"
)

// 用用户提供的 .gitignore 内容，验证翻译结果
const refGitignore = `
# docs/ 下是内部笔记，不入库；但贡献者要看的规范文档必须能被提交
docs/*
!docs/CODE-STYLE.md
.hermes/
MIGRATION.md
# Go 后端运行时数据
server/storage/
server/tmp/
.air.toml
server/.air.toml
server/*.exe
server/controlhub-server-*
build/
.env
server/internal/web/dist/*
!server/internal/web/dist/.gitkeep
frontend/dist/
frontend/node_modules/
server/node_modules/
sub_diag/
`

func TestTranslateReference(t *testing.T) {
	got := GitignoreToRclone(refGitignore)
	want := []string{
		// 包含规则（先输出，first-match-wins）
		"+ /docs/CODE-STYLE.md",
		"+ /server/internal/web/dist/.gitkeep",
		// 排除规则
		"- /docs/**",
		"- .hermes/**",
		"- MIGRATION.md",
		"- /server/storage/**",
		"- /server/tmp/**",
		"- .air.toml",
		"- /server/.air.toml",
		"- /server/*.exe",
		"- /server/controlhub-server-*",
		"- build/**",
		"- .env",
		"- /server/internal/web/dist/**",
		"- /frontend/dist/**",
		"- /frontend/node_modules/**",
		"- /server/node_modules/**",
		"- sub_diag/**",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("translation mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestConvertPattern(t *testing.T) {
	cases := map[string]string{
		"docs/*":            "/docs/**",
		"!docs/CODE-STYLE.md": "", // 由调用方去 !，这里测纯模式用下面的
	}
	_ = cases
	checks := []struct{ in, want string }{
		{"/rootonly.txt", "/rootonly.txt"},
		{"*.tmp", "*.tmp"},
		{"node_modules/", "node_modules/**"},
		{"a/b/c", "/a/b/c"},
		{"dir/*", "/dir/**"},
		{"plain.txt", "plain.txt"},
	}
	for _, c := range checks {
		if got := convertPattern(c.in); got != c.want {
			t.Errorf("convertPattern(%q)=%q want %q", c.in, got, c.want)
		}
	}
}

func TestFiltersFileContent(t *testing.T) {
	out := FiltersFileContent("*.log\nkeep.txt\n")
	if !strings.Contains(out, "- /.nosy") {
		t.Error("should auto-exclude /.nosy")
	}
	if !strings.Contains(out, "- *.log") || !strings.Contains(out, "- keep.txt") {
		t.Errorf("bad content: %s", out)
	}
}
