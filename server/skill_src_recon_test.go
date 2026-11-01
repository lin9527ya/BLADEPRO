package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lin9527ya/Norma/skill"
)

// TestSrcReconSkillPack 校验仓库内的 src-recon 技能包能被加载：
// frontmatter 合法、name 与目录名一致（可见性按目录名匹配、Registry 按 name 注册，
// 二者不一致会出现"可见但调用不到/解锁不了 MCP"）、mcps 指向 ENScan、
// references 与 tools 数量不漂。
func TestSrcReconSkillPack(t *testing.T) {
	root := filepath.Join("..", "skills")
	dir := filepath.Join(root, "src-recon")

	md, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("读取 SKILL.md: %v", err)
	}
	if err := validateSkillFrontmatter(md); err != nil {
		t.Fatalf("frontmatter 不合法: %v", err)
	}

	reg, err := skill.LoadDir(root)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	s, ok := reg.Get("src-recon")
	if !ok {
		t.Fatal("src-recon 未被加载（frontmatter name 必须与目录名一致）")
	}
	if len(s.MCPs) != 1 || s.MCPs[0] != "ENScan" {
		t.Fatalf("技能应只解锁 ENScan MCP，实际: %v", s.MCPs)
	}
	// Norma 的列表按字节截断 description，中文超长会被切在字符中间。
	if len(s.Description) > 500 {
		t.Fatalf("description 过长（%d 字节），会被列表截断", len(s.Description))
	}

	refs, err := filepath.Glob(filepath.Join(dir, "references", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 9 {
		t.Fatalf("references 应为 9 篇，实际 %d：%v", len(refs), refs)
	}
	// 浏览器手采兜底挂在 playwright-cli 上：它必须声明 mcps: browser，且 frontmatter 合法。
	bmd, err := os.ReadFile(filepath.Join(root, "playwright-cli", "SKILL.md"))
	if err != nil {
		t.Fatalf("读取 playwright-cli/SKILL.md: %v", err)
	}
	if err := validateSkillFrontmatter(bmd); err != nil {
		t.Fatalf("playwright-cli frontmatter 不合法: %v", err)
	}
	pw, ok := reg.Get("playwright-cli")
	if !ok {
		t.Fatal("playwright-cli 未被加载")
	}
	if len(pw.MCPs) != 1 || pw.MCPs[0] != "browser" {
		t.Fatalf("playwright-cli 应解锁 browser MCP，实际: %v", pw.MCPs)
	}

	entries, err := os.ReadDir(filepath.Join(dir, "tools"))
	if err != nil {
		t.Fatalf("tools 目录: %v", err)
	}
	var scripts int
	for _, e := range entries {
		if !e.IsDir() && !strings.HasSuffix(e.Name(), ".pyc") {
			scripts++
		}
	}
	if scripts != 9 {
		t.Fatalf("tools 应为 9 个脚本，实际 %d", scripts)
	}
}
