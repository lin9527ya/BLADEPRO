package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
	"github.com/lin9527ya/bladepro/db"
)

// P-Q 统一知识工具（Ported from quiver, AGPL-3.0；见 docs/third-party/QUIVER.md）。
//
// 四个工具取代原来的 lookup_knowledge / record_knowledge / knowledge_feedback，
// 并合并 quiver 的 query_memory / record_memory_proposal / save_ammo / mark_memory_stale：
//   - lookup_knowledge：指纹模式（asset_id / 指纹字段）或自由文本模式；
//   - record_knowledge：写弹药（task 级直写、global 转审批）或打法卡（仅 confidence=high）；
//   - propose_knowledge：跨任务经验/方法论/厂商规则，一律进审批；
//   - knowledge_feedback：worked / failed / verified / stale。
//
// 纪律：注入块不计数；只有 lookup_knowledge 命中的条目才 TouchKnowledge。

func (t *ToolSet) lookupKnowledge() actool.CoreTool {
	return readTool("lookup_knowledge",
		"查【跨任务知识库】：同一目标/厂商以前沉淀的记忆、弹药与指纹打法。\n"+
			"两种用法：\n"+
			"  1) 指纹模式：传 asset_id（推荐，平台从资产库取真实指纹）或 technologies/favicon_mmh3/service_name/port，\n"+
			"     返回按匹配度排序的打法卡（含完整步骤）；命中不代表目标一定有洞，仍要实际触发。\n"+
			"  2) 文本模式：传 q（自由文本）+ 可选 types/scope/company_id，检索记忆、弹药、知识条目。\n"+
			"用法反馈：验证后用 knowledge_feedback(id, outcome=worked|failed) 回报。",
		obj(map[string]any{
			"q":            str("自由文本检索（匹配 key/title/summary/body）"),
			"asset_id":     idp("资产 id（指纹模式；从资产库取 technologies/favicon/port）"),
			"technologies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "技术栈（可选）"},
			"favicon_mmh3": str("favicon MMH3 哈希（可选，最强信号）"),
			"service_name": str("服务名（可选）"),
			"port":         intp("端口（可选）"),
			"page_title":   str("页面标题（可选，弱信号）"),
			"types": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
				"description": "限定条目类型：memory/ammo/technique/methodology/vendor_rule/fact/conclusion/asset_hint/playbook"},
			"scope":      str("限定范围：company/global/task（默认按任务与公司自动选择）"),
			"company_id": idp("公司 id（可选；默认用任务关联公司）"),
			"limit":      intp("返回上限，默认 5（指纹模式默认 3）"),
		}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("lookup_knowledge 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				Q            string          `json:"q"`
				AssetID      json.RawMessage `json:"asset_id"`
				Technologies []string        `json:"technologies"`
				FaviconMMH3  string          `json:"favicon_mmh3"`
				ServiceName  string          `json:"service_name"`
				Port         int             `json:"port"`
				PageTitle    string          `json:"page_title"`
				Types        []string        `json:"types"`
				Scope        string          `json:"scope"`
				CompanyID    json.RawMessage `json:"company_id"`
				Limit        int             `json:"limit"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			assetID := pid(a.AssetID)
			fp := db.PlaybookFingerprint{
				Technologies: a.Technologies, FaviconMMH3: a.FaviconMMH3,
				ServiceName: a.ServiceName, Port: a.Port, PageTitle: a.PageTitle,
			}
			var assetView map[string]any
			if assetID > 0 {
				var assets []*db.Asset
				var err error
				if t.taskID > 0 {
					assets, err = t.as.GetByIDsInScope(t.taskID, []int64{assetID})
				} else {
					assets, err = t.as.GetByIDs([]int64{assetID})
				}
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				if len(assets) == 0 {
					return actool.Errorf(fmt.Sprintf("asset_id=%d 不在本任务范围内或不存在", assetID)), nil
				}
				fp = fingerprintFromAssets(fp, assets)
				assetView = map[string]any{
					"id": assets[0].ID, "type": assets[0].Type, "url": assets[0].URL,
					"technologies": assets[0].Technologies, "favicon_mmh3": assets[0].FaviconMMH3,
				}
			}
			fp = fp.Normalize()
			fingerprintMode := len(fp.Technologies) > 0 || fp.FaviconMMH3 != "" || fp.ServiceName != ""

			if fingerprintMode {
				limit := a.Limit
				if limit <= 0 {
					limit = 3
				}
				matches, err := t.as.MatchKnowledgePlaybooks(fp, limit)
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				ids := make([]int64, 0, len(matches))
				for _, m := range matches {
					ids = append(ids, m.ID)
				}
				_ = t.as.TouchKnowledge(ids)
				out := map[string]any{
					"fingerprint": fp, "matches": matches, "count": len(matches),
					"note": "匹配只是先验：确认指纹一致后按 body_md 实际验证；" +
						"验证后用 knowledge_feedback 回报 worked/failed。空结果不代表目标安全。",
				}
				if assetView != nil {
					out["asset"] = assetView
				}
				return jsonResult(out)
			}

			q := strings.TrimSpace(a.Q)
			if q == "" {
				return actool.Errorf("需要 q（文本检索），或至少给 asset_id / technologies / favicon_mmh3 / service_name 之一（指纹模式）"), nil
			}
			types := a.Types
			for i, typ := range types {
				if n := db.NormalizeKnowledgeType(typ); n != "" {
					types[i] = n
				}
			}
			limit := a.Limit
			if limit <= 0 {
				limit = 5
			}
			companyID := pid(a.CompanyID)
			var entries []db.KnowledgeEntry
			switch strings.ToLower(strings.TrimSpace(a.Scope)) {
			case db.KnowledgeScopeGlobal:
				entries, _ = t.as.LookupKnowledge(db.KnowledgeQuery{
					Scope: db.KnowledgeScopeGlobal, Types: types, Text: q, Limit: limit})
			case db.KnowledgeScopeTask:
				entries, _ = t.as.LookupKnowledge(db.KnowledgeQuery{
					Scope: db.KnowledgeScopeTask, TaskID: t.taskID, Types: types, Text: q, Limit: limit})
			default:
				if companyID <= 0 {
					companyID = t.knowledgeCompanyID()
				}
				if companyID > 0 {
					entries, _ = t.as.LookupKnowledge(db.KnowledgeQuery{
						Scope: db.KnowledgeScopeCompany, CompanyID: companyID, IncludeGlobal: true,
						Types: types, Text: q, Limit: limit})
				} else {
					entries, _ = t.as.LookupKnowledge(db.KnowledgeQuery{
						Scope: db.KnowledgeScopeGlobal, Types: types, Text: q, Limit: limit})
				}
			}
			ids := make([]int64, 0, len(entries))
			for _, e := range entries {
				ids = append(ids, e.ID)
			}
			_ = t.as.TouchKnowledge(ids)
			return jsonResult(map[string]any{
				"query": q, "entries": entries, "count": len(entries),
				"note": "知识是背景先验，不是本任务结论：与本任务实测冲突时以实测为准，并用 knowledge_feedback 纠错。",
			})
		})
}

// knowledgeCompanyID 解析本任务关联公司（显式 scope 优先），供工具默认使用。
func (t *ToolSet) knowledgeCompanyID() int64 {
	if t.cs == nil || t.taskID <= 0 {
		return 0
	}
	profs, err := t.cs.ResolveTaskCompanies(t.taskID)
	if err != nil || len(profs) == 0 {
		return 0
	}
	return profs[0].CompanyID
}

func (t *ToolSet) recordKnowledge() actool.CoreTool {
	return writeTool("record_knowledge",
		"写【跨任务知识】：弹药（ammo）或打法卡（playbook）。\n"+
			"  · ammo：对象标识/接口规律/参数规律/payload/凭据线索。scope=task 直接写入；scope=global 转人工审批。\n"+
			"  · playbook：只有 confidence=high（真实打穿、可复现）才收；指纹优先由 asset_ids 从资产库取。\n"+
			"记忆/方法论/厂商规则请用 propose_knowledge（一律进审批）。脱敏：不写凭据明文、客户域名/IP、人员信息。",
		obj(map[string]any{
			"entry_type":     str("ammo 或 playbook"),
			"scope":          str("task / company / global（ammo 默认 task；playbook 固定 global）"),
			"title":          str("标题（打法名或弹药摘要）"),
			"summary":        str("一句话摘要（必填）"),
			"body_md":        str("正文（打法步骤 / 弹药上下文，Markdown）"),
			"key":            str("键（可选，如 url_pattern / param_rule）"),
			"kind":           str("细分类型（ammo_kind：id|username|phone|openid|order_id|url_pattern|param_pattern|payload_effective|cookie|token）"),
			"confidence":     str("置信度 high/medium/low（打法卡必须 high）"),
			"technique_key":  str("打法手法 slug（playbook 用；留空用标题归一）"),
			"asset_ids":      map[string]any{"type": "array", "items": map[string]any{"type": "integer"}, "description": "指纹来源资产（推荐）"},
			"technologies":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "手工指纹（资产库没有时）"},
			"favicon_mmh3":   str("手工 favicon 哈希（可选）"),
			"service_name":   str("手工服务名（可选）"),
			"port":           intp("手工端口（可选）"),
			"page_title":     str("手工页面标题（可选）"),
			"evidence_ref":   str("证据引用（节点 id / 流量 id / 报告路径）"),
			"source_node_id": idp("来源图节点 id（可选）"),
		}, "entry_type", "summary"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("record_knowledge 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				EntryType    string          `json:"entry_type"`
				Scope        string          `json:"scope"`
				Title        string          `json:"title"`
				Summary      string          `json:"summary"`
				BodyMD       string          `json:"body_md"`
				Key          string          `json:"key"`
				Kind         string          `json:"kind"`
				Confidence   string          `json:"confidence"`
				TechniqueKey string          `json:"technique_key"`
				AssetIDs     []int64         `json:"asset_ids"`
				Technologies []string        `json:"technologies"`
				FaviconMMH3  string          `json:"favicon_mmh3"`
				ServiceName  string          `json:"service_name"`
				Port         int             `json:"port"`
				PageTitle    string          `json:"page_title"`
				EvidenceRef  string          `json:"evidence_ref"`
				SourceNodeID json.RawMessage `json:"source_node_id"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			entryType := db.NormalizeKnowledgeType(a.EntryType)
			if entryType != db.KnowledgeTypeAmmo && entryType != db.KnowledgeTypePlaybook {
				return actool.Errorf("record_knowledge 只接受 entry_type=ammo 或 playbook；其余用 propose_knowledge"), nil
			}
			summary := strings.TrimSpace(a.Summary)
			if summary == "" {
				return actool.Errorf("summary 必填"), nil
			}
			if entryType == db.KnowledgeTypePlaybook {
				if db.NormalizeConfidence(a.Confidence) != "high" {
					return actool.Errorf("打法卡只收 confidence=high（真实打穿、可当场复现）"), nil
				}
				fp := db.PlaybookFingerprint{
					Technologies: a.Technologies, FaviconMMH3: a.FaviconMMH3,
					ServiceName: a.ServiceName, Port: a.Port, PageTitle: a.PageTitle,
				}
				if len(a.AssetIDs) > 0 {
					var assets []*db.Asset
					var err error
					if t.taskID > 0 {
						assets, err = t.as.GetByIDsInScope(t.taskID, a.AssetIDs)
					} else {
						assets, err = t.as.GetByIDs(a.AssetIDs)
					}
					if err != nil {
						return actool.Errorf(err.Error()), nil
					}
					if len(assets) == 0 {
						return actool.Errorf("asset_ids 均不在本任务范围内或不存在"), nil
					}
					fp = fingerprintFromAssets(fp, assets)
				}
				fp = fp.Normalize()
				if len(fp.Technologies) == 0 && fp.FaviconMMH3 == "" && fp.ServiceName == "" {
					return actool.Errorf("打法卡需要指纹：传 asset_ids，或至少给 technologies/favicon_mmh3/service_name"), nil
				}
				fpJSON, _ := json.Marshal(fp)
				technique := strings.TrimSpace(a.TechniqueKey)
				if technique == "" {
					technique = db.NormalizeTechniqueKey(a.Title)
				}
				entry, inserted, err := t.as.UpsertKnowledgePlaybook(db.KnowledgeEntry{
					Title: a.Title, Summary: summary, BodyMD: a.BodyMD, Key: technique,
					Fingerprint: fpJSON, FingerprintKey: fp.Key(), TechniqueKey: technique,
					Confidence: "high", EvidenceRef: a.EvidenceRef,
					SourceTaskID: fmt.Sprintf("%d", t.taskID), SourceNodeID: pid(a.SourceNodeID),
				})
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{
					"entry": entry, "inserted": inserted,
					"note": "已写入统一知识库；验证结果可用 knowledge_feedback(id, outcome=worked|failed) 回流。",
				})
			}

			scope := strings.ToLower(strings.TrimSpace(a.Scope))
			if scope == "" {
				scope = db.KnowledgeScopeTask
			}
			if scope == db.KnowledgeScopeGlobal {
				payload, _ := json.Marshal(map[string]any{
					"entry_type": db.KnowledgeTypeAmmo, "scope": db.KnowledgeScopeGlobal,
					"key": a.Key, "title": a.Title, "summary": summary, "body_md": a.BodyMD,
					"ammo_kind": a.Kind, "confidence": db.NormalizeConfidence(a.Confidence),
					"source_task_id": fmt.Sprintf("%d", t.taskID), "evidence_ref": a.EvidenceRef,
				})
				ap, err := t.as.CreateApproval(db.ApprovalKindAmmoGlobal, payload, "全局弹药提案")
				if err != nil {
					return actool.Errorf(err.Error()), nil
				}
				return jsonResult(map[string]any{
					"approval_id": ap.ID, "status": ap.Status,
					"note": "全局弹药需人工审批后生效；本任务内可直接用 scope=task 立即复用。",
				})
			}
			entry, err := t.as.InsertKnowledgeEntry(db.KnowledgeEntry{
				Scope: scope, TaskID: t.taskID, EntryType: db.KnowledgeTypeAmmo,
				Key: a.Key, Title: a.Title, Summary: summary, BodyMD: a.BodyMD,
				AmmoKind: a.Kind, Confidence: db.NormalizeConfidence(a.Confidence),
				SourceTaskID: fmt.Sprintf("%d", t.taskID), SourceNodeID: pid(a.SourceNodeID),
				EvidenceRef: a.EvidenceRef,
			})
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"entry": entry, "note": "弹药已写入，本任务后续轮次会注入。"})
		})
}

func (t *ToolSet) proposeKnowledge() actool.CoreTool {
	return writeTool("propose_knowledge",
		"提【跨任务知识提案】：记忆 / 技术 / 方法论 / 厂商规则。提案进人工审批，批准后才生效——\n"+
			"这是防「记忆污染」的闸：不要试图直接写库。全局弹药也用这个（entry_type=ammo + scope=global）。",
		obj(map[string]any{
			"entry_type":   str("memory / technique / methodology / vendor_rule / ammo / fact / conclusion / asset_hint"),
			"scope":        str("company（默认，需 company_id）或 global"),
			"company_id":   idp("公司 id（scope=company 必填；默认用任务关联公司）"),
			"key":          str("键（如 waf_profile / param_rule / vendor_pref）"),
			"title":        str("标题（可选）"),
			"summary":      str("摘要（必填）"),
			"body_md":      str("正文（可选，Markdown）"),
			"confidence":   str("置信度 observed/inferred 或 high/medium/low"),
			"evidence_ref": str("证据引用（节点/流量/链接）"),
		}, "entry_type", "summary"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("propose_knowledge 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				EntryType   string          `json:"entry_type"`
				Scope       string          `json:"scope"`
				CompanyID   json.RawMessage `json:"company_id"`
				Key         string          `json:"key"`
				Title       string          `json:"title"`
				Summary     string          `json:"summary"`
				BodyMD      string          `json:"body_md"`
				Confidence  string          `json:"confidence"`
				EvidenceRef string          `json:"evidence_ref"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			entryType := db.NormalizeKnowledgeType(a.EntryType)
			if entryType == "" || entryType == db.KnowledgeTypePlaybook {
				return actool.Errorf("entry_type 不合法（打法卡用 record_knowledge）"), nil
			}
			summary := strings.TrimSpace(a.Summary)
			if summary == "" {
				return actool.Errorf("summary 必填"), nil
			}
			scope := strings.ToLower(strings.TrimSpace(a.Scope))
			if scope == "" {
				scope = db.KnowledgeScopeCompany
			}
			companyID := pid(a.CompanyID)
			if companyID <= 0 {
				companyID = t.knowledgeCompanyID()
			}
			if scope == db.KnowledgeScopeCompany && companyID <= 0 {
				return actool.Errorf("scope=company 需要 company_id（本任务没有关联公司时请用 scope=global）"), nil
			}
			kind := db.ApprovalKindKnowledge
			if entryType == db.KnowledgeTypeAmmo && scope == db.KnowledgeScopeGlobal {
				kind = db.ApprovalKindAmmoGlobal
			}
			payload, _ := json.Marshal(map[string]any{
				"entry_type": entryType, "scope": scope, "company_id": companyID,
				"key": a.Key, "title": a.Title, "summary": summary, "body_md": a.BodyMD,
				"confidence":     db.NormalizeConfidence(a.Confidence),
				"source_task_id": fmt.Sprintf("%d", t.taskID), "evidence_ref": a.EvidenceRef,
			})
			ap, err := t.as.CreateApproval(kind, payload, "知识提案")
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{
				"approval_id": ap.ID, "status": ap.Status,
				"note": "已进入审批队列；批准后才会写入知识库并对后续任务生效。",
			})
		})
}

func (t *ToolSet) knowledgeFeedback() actool.CoreTool {
	return writeTool("knowledge_feedback",
		"回报一条知识/打法卡的实战结果：worked / failed / verified / stale。\n"+
			"worked/failed 累计成功率；verified 更新复核时间；stale 标记失效（采信前需重新验证）。",
		obj(map[string]any{
			"id":      idp("知识条目 id（lookup_knowledge 返回）"),
			"outcome": str("worked / failed / verified / stale"),
			"note":    str("备注（可选）：为什么有效/失效"),
		}, "id", "outcome"),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("knowledge_feedback 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				ID      int64  `json:"id"`
				Outcome string `json:"outcome"`
				Note    string `json:"note"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.ID <= 0 {
				return actool.Errorf("id 必填"), nil
			}
			if err := t.as.FeedbackKnowledge(a.ID, a.Outcome, a.Note); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"ok": true, "id": a.ID, "outcome": strings.ToLower(strings.TrimSpace(a.Outcome))})
		})
}
