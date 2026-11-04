package agent

import (
	"strings"
)

// P-Q 深度分级（Ported from quiver constraints.go, AGPL-3.0）。
//
// depthBlock 定义「这次可以打到多深」：L1 证明存在 / L2 证明危害范围 / L3 证明控制力，
// 外加任何等级都不做的禁止区。它是提示词约束，和拦截层（ask/deny 规则）并行且互补：
// 拦截层保证"越线会被拦"，这里的等级保证"根本不去试"。
func depthBlock(taskID int64) string {
	d := 2
	if DepthProvider != nil {
		v := DepthProvider(taskID)
		if v < 0 {
			return "" // 梯度开关关闭
		}
		if v >= 1 && v <= 3 {
			d = v
		}
	}
	var b strings.Builder
	b.WriteString("\n\n【测试深度分级（最高优先级，与操作约束同级）】：本任务生效深度：")
	b.WriteString(depthTierName(d))
	b.WriteString("。目标深度=当前等级，证明到该等级即视为该方向已完成；超出等级的动作不要尝试（平台会拦截并转人工，硬闯只浪费轮次）。等级定义：\n")
	b.WriteString("- L1 证明存在：反射回显、报错信息、sleep(2)-sleep(5) 时间盲、上传 txt 验证。\n")
	b.WriteString("- L2 证明危害范围：取得足以证明能访问到不该访问的数据/功能的证据即收（禁止批量获取）；读数据库名/当前用户、列目录、读单个敏感文件同属此级。\n")
	b.WriteString("- L3 证明控制力：执行命令（仅 id/whoami/hostname 级只读命令）、修改自己测试账号字段——需人工审批，先在产出里说明理由。\n")
	b.WriteString("- 禁止区（任何等级都不做，申请也不做）：批量拉取/翻页遍历/整库导出；删改非测试数据；写文件/持久化/反弹外带；内网横向；DoS/sleep≥10；触发真实短信；真实支付流程。")
	if d == 1 {
		b.WriteString("\n本任务为 L1 封顶：L2 的取样动作用 L1 替代——只证明可触达（如响应差异、计数），不取样本数据内容。")
	}
	if d == 3 {
		b.WriteString("\n本任务允许 L3：执行命令仅限只读白名单（id/whoami/hostname/uname），改数据仅限自己测试账号；更深的动作仍属禁止区。")
	}
	return b.String()
}

// depthTierName 给等级一个可读标签。
func depthTierName(d int) string {
	switch d {
	case 1:
		return "L1（证明存在）"
	case 3:
		return "L3（证明控制力）"
	default:
		return "L2（证明危害范围）"
	}
}

// vendorBlock 渲染厂商/公司上下文（域名归属、测试窗口、不收洞型、定级口径）。
// 空字符串表示未配置或没有可归因的公司。
func vendorBlock(taskID int64) string {
	if VendorProvider == nil {
		return ""
	}
	return VendorProvider(taskID)
}
