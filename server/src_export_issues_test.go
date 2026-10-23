package server

import (
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/report"
)

// TestSrcExportIssues 钉住导出前的正文门：占位符/过短正文必须被报出来，
// 齐备的八块正文不应误报。导出本身不阻断，问题是让人在下载时就看见。
func TestSrcExportIssues(t *testing.T) {
	placeholder := report.SrcInput{
		Title:       "某系统越权读取",
		Description: "待补",
		Impact:      "TODO",
		Fix:         "待补",
	}
	issues := srcExportIssues(placeholder)
	if len(issues) == 0 {
		t.Fatal("占位符正文必须被导出校验报出来")
	}
	joined := strings.Join(issues, "；")
	if !strings.Contains(joined, "描述") && !strings.Contains(joined, "待补") && !strings.Contains(joined, "TODO") {
		t.Fatalf("拒绝理由要能指向具体字段：%s", joined)
	}

	full := report.SrcInput{
		Title:       "某系统订单越权读取",
		Severity:    "high",
		Description: strings.Repeat("我用自己的账号读取到了其他租户的订单详情，服务端只校验登录不校验归属。", 2),
		Impact:      strings.Repeat("任意登录用户可批量读取他人订单与收货信息，造成数据泄露。", 2),
		Fix:         strings.Repeat("在订单详情接口按 tenant_id 校验归属，并补充越权回归测试。", 2),
	}
	if got := srcExportIssues(full); len(got) != 0 {
		t.Fatalf("齐备正文不应误报：%v", got)
	}
}
