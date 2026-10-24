package server

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestSrcVerdictAdviceAndNote(t *testing.T) {
	if got := srcVerdictAdvice(db.SrcVerdictNotReproducible); !strings.Contains(got, "独立复验") {
		t.Fatalf("无法复现的建议应指向独立复验：%q", got)
	}
	if got := srcVerdictAdvice(db.SrcVerdictDuplicate); !strings.Contains(got, "查重") {
		t.Fatalf("重复的建议应指向查重：%q", got)
	}
	sub := &db.SrcSubmission{
		Status:        db.SrcSubRejected,
		VerdictReason: db.SrcVerdictInsufficient,
		Note:          "缺截图",
	}
	note := srcVerdictNote(sub)
	if !strings.Contains(note, "信息不全") || !strings.Contains(note, "缺截图") {
		t.Fatalf("反馈说明缺原因码或备注：%q", note)
	}
}

func TestPlaybookFingerprintForAssets(t *testing.T) {
	port := 8443
	assets := map[int64]*db.Asset{
		1: {Technologies: []string{"PHP"}, PageTitle: "Portal", Port: &port},
		2: {Technologies: []string{"WordPress"}, FaviconMMH3: "-555"},
	}
	fp := playbookFingerprintForAssets(assets, []int64{1, 2})
	if len(fp.Technologies) != 2 || fp.FaviconMMH3 != "-555" || fp.Port != 8443 || fp.PageTitle != "Portal" {
		t.Fatalf("指纹聚合不对：%+v", fp)
	}
	if !hasPlaybookSignal(fp) {
		t.Fatal("有技术栈/端口时应视为有信号")
	}
	if hasPlaybookSignal(playbookFingerprintForAssets(nil, nil)) {
		t.Fatal("空资产不该判定为有信号")
	}
}
