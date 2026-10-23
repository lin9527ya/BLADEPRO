package srcmode

import "strings"

// 提交块（src-dig vuln-report-format.md §〇）：平台表单要的那几项，直接抄。
//
// 为什么由机器生成：测试源 IP、平台名、映射后的等级这几项都是"抄错就白干"的字段
// （技能里记录过"换网络后抄旧报告的 IP 必错"），而它们的来源本来就在任务快照里。

// SubmitInfo 渲染【提交信息】块；快照里没有任何提交字段时返回空串（不硬塞空块）。
func SubmitInfo(doc ModeDoc, severityLabel string) string {
	if strings.TrimSpace(doc.Platform) == "" && strings.TrimSpace(doc.Program) == "" &&
		strings.TrimSpace(doc.EgressIP) == "" && strings.TrimSpace(doc.TestAccounts) == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 【提交信息】\n")
	if v := strings.TrimSpace(doc.Platform); v != "" {
		b.WriteString("- 平台：" + v + "\n")
	}
	if v := strings.TrimSpace(doc.Program); v != "" {
		b.WriteString("- 所属应用/项目：" + v + "\n")
	}
	if v := strings.TrimSpace(severityLabel); v != "" {
		b.WriteString("- 危害等级：" + v + "\n")
	}
	if v := strings.TrimSpace(doc.EgressIP); v != "" {
		b.WriteString("- 测试源IP：" + v + "（提交前当场复核一次：换网络就会变）\n")
	}
	if v := strings.TrimSpace(doc.TestAccounts); v != "" {
		b.WriteString("- 测试账号：" + v + "\n")
	} else {
		b.WriteString("- 测试账号：无需账号（未授权类）；越权类需自备，缺账号会在审核阶段被挂起\n")
	}
	return b.String()
}
