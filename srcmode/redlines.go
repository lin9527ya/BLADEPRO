package srcmode

import (
	"strconv"
	"strings"
)

// 内建安全红线（对齐 src-dig 的 SKILL.md §安全红线 1–4 与 dig-scope §4.2.2）。
//
// 这些不是"平台规则"——任何 SRC 任务都生效，因为它们是"挖洞这个动作本身的边界"：
// 不踢用户会话、不在别人账号上完成写入、不造成真实资损、不做内网横向、不挖 CORS。
// 文字进提示词，能机器化的（会话红线）同时编译成命令级规则。

// Redline 是一条红线。
type Redline struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Redlines 是全部内建红线（顺序即提示词顺序）。
var Redlines = []Redline{
	{Key: "session", Text: "禁止登出/注销/吊销：不要调 /logout /signout /revoke，也不要测「退出后会话还在不在」——用户会话必须全程有效。"},
	{Key: "others", Text: "别人账号只探不发：把 uid/email/phone 换成别人的，只做到「提交出去、看服务端回包接不接受」为止，不在第三方账号上完成最终写入。"},
	{Key: "rebind", Text: "改密/改绑可探（拿掉旧验证看是否放行），通过后立刻改回；改不回去就停在回包，不要把用户名下的密码/邮箱留在新值上。"},
	{Key: "money", Text: "券/库存/金额只改小、改负、改小数来证明校验缺失；禁止真实资损、禁止批量领取/清库存。"},
	{Key: "batch", Text: "禁止批量：越权读取真实数据不超过 5 组（平台档案更严的按档案），最小证明即停。"},
	{Key: "intranet", Text: "禁止内网渗透与横向：打穿 Web 即止，不扫内网、不碰非公网目标。"},
	{Key: "dos", Text: "禁止 DoS/洪水：不做拒绝服务，不用会打出大批量流量的扫描方式。"},
	{Key: "cors", Text: "CORS 永久不挖：不测、不写、不报告。"},
}

// RedlineBlock 渲染成提示词里的一段。
func RedlineBlock() string {
	if len(Redlines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("【安全红线（不可违反）】\n")
	for i, r := range Redlines {
		b.WriteString(strconv.Itoa(i+1) + ". " + r.Text + "\n")
	}
	return b.String()
}

// BuiltinRules 返回可以机器化的红线（目前只有会话红线）。
// 它会被并进每个 SRC 任务的探索策略 → 编译成命令门规则：说了不算，拦得住才算。
func BuiltinRules() []ProgramRule {
	return []ProgramRule{{
		Label:   "SRC 会话红线：禁止登出/注销/吊销",
		Pattern: `(?i)(/logout|/signout|/sign-out|/revoke|/invalidate|退出登录|注销账号|吊销令牌)`,
		Regex:   true,
	}}
}
