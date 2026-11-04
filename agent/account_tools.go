package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/db"
)

// P-Q 账号与定级工具（移植自 quiver test_accounts / rating_rules）。
//
//   - get_account_credential：按账号 id 取明文凭据（用户明确允许）。它被登记为
//     action 工具，因此受作业时间窗/拦截规则/审计约束；返回值不得写进报告或知识。
//   - list_rating_rules / save_rating_rule：公司/通用定级口径的读与写（写由 ops agent 使用）。

func (t *ToolSet) getAccountCredentialTool() actool.CoreTool {
	return readTool("get_account_credential",
		"取一个测试账号的**明文凭据**（cookie/token/password），用于授权范围内的多身份/越权测试。\n"+
			"纪律：只在本任务授权范围内使用；不要把凭据写进报告、知识、截图或命令行历史；\n"+
			"用完不要把明文回显给用户。password 类型目前只做可达性探活，平台不做表单登录。",
		obj(map[string]any{
			"account_id": idp("测试账号 id（来自 /api/accounts 或任务绑定的 account_ids）"),
		}, "account_id"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("get_account_credential 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				AccountID int64 `json:"account_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.AccountID <= 0 {
				return actool.Errorf("account_id 必填"), nil
			}
			acct, cred, err := t.as.GetTestAccountCredential(a.AccountID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if acct == nil {
				return actool.Errorf(fmt.Sprintf("账号 %d 不存在", a.AccountID)), nil
			}
			if acct.Status != "active" {
				return actool.Errorf(fmt.Sprintf("账号 %d 状态为 %s，不能使用", a.AccountID, acct.Status)), nil
			}
			return jsonResult(map[string]any{
				"account_id": acct.ID, "label": acct.Label, "role_tier": acct.RoleTier,
				"company_id": acct.CompanyID, "cred_type": acct.CredType, "credential": cred,
				"note": "凭据仅用于本任务授权范围内的测试；不要写入报告/知识/日志。",
			})
		})
}

func (t *ToolSet) listRatingRulesTool() actool.CoreTool {
	return readTool("list_rating_rules",
		"查公司/通用定级口径（洞型 → 建议等级）。公司规则优先于通用规则；report_finding 的等级应与之一致，不一致时在正文说明理由。",
		obj(map[string]any{
			"company_id": idp("公司 id（可选；默认用任务关联公司）"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("list_rating_rules 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				CompanyID int64 `json:"company_id"`
			}
			_ = json.Unmarshal(in, &a)
			if a.CompanyID <= 0 {
				a.CompanyID = t.knowledgeCompanyID()
			}
			rules, err := t.as.ListRatingRules(a.CompanyID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"company_id": a.CompanyID, "rules": rules, "count": len(rules)})
		})
}

func (t *ToolSet) saveRatingRuleTool() actool.CoreTool {
	return writeTool("save_rating_rule",
		"录入/更新一条定级口径（公司级或通用）。运营/审核 agent 使用；重复 (公司, 洞型) 会覆盖。",
		obj(map[string]any{
			"company_id": idp("公司 id（留空 = 通用规则）"),
			"vulnclass":  str("洞型 slug（如 idor / ssrf / stored-xss）"),
			"severity":   str("等级 critical/high/medium/low/info"),
			"title":      str("标题（可选）"),
			"note":       str("口径说明（可选）"),
		}, "vulnclass", "severity"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("save_rating_rule 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				CompanyID int64  `json:"company_id"`
				VulnClass string `json:"vulnclass"`
				Severity  string `json:"severity"`
				Title     string `json:"title"`
				Note      string `json:"note"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if strings.TrimSpace(a.VulnClass) == "" || strings.TrimSpace(a.Severity) == "" {
				return actool.Errorf("vulnclass 与 severity 必填"), nil
			}
			rule, err := t.as.UpsertRatingRule(a.CompanyID, a.VulnClass, a.Severity, a.Title, a.Note)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"rule": rule, "note": "已保存；后续 report_finding 的建议等级应与之对齐。"})
		})
}

// 确保 db 包类型被引用（工具参数使用 db.RatingRule 的 JSON 形状）。
var _ = db.RatingRule{}
