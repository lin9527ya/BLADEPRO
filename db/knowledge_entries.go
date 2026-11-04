package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// P-Q 统一知识条目（移植自 quiver 的 memory/ammo/knowledge，与 BLADEPRO 的
// company_knowledge / attack_playbooks 合并为一张表）。
//
// 纪律：
//   - 运行时只读写本表；旧表保留数据但冻结（回滚靠迁移前备份）。
//   - 写入侧按 entry_type 白名单归一；scope 与 owner 必须自洽（库里有 CHECK）。
//   - 注入面不 bump hits（保护 system 前缀缓存）；只有显式检索才 TouchKnowledge。

const (
	KnowledgeScopeCompany = "company"
	KnowledgeScopeGlobal  = "global"
	KnowledgeScopeTask    = "task"
)

const (
	KnowledgeTypeFact         = "fact"
	KnowledgeTypeConclusion   = "conclusion"
	KnowledgeTypeAssetHint    = "asset_hint"
	KnowledgeTypePlaybookHint = "playbook_hint"
	KnowledgeTypeTechnique    = "technique"
	KnowledgeTypeVendorRule   = "vendor_rule"
	KnowledgeTypeMethodology  = "methodology"
	KnowledgeTypeMemory       = "memory"
	KnowledgeTypeAmmo         = "ammo"
	KnowledgeTypePlaybook     = "playbook"
)

const (
	KnowledgeStatusActive     = "active"
	KnowledgeStatusStale      = "stale"
	KnowledgeStatusRetired    = "retired"
	KnowledgeStatusDeprecated = "deprecated"
)

// KnowledgeTypes 是全部合法 entry_type（写入白名单）。
var KnowledgeTypes = []string{
	KnowledgeTypeFact, KnowledgeTypeConclusion, KnowledgeTypeAssetHint, KnowledgeTypePlaybookHint,
	KnowledgeTypeTechnique, KnowledgeTypeVendorRule, KnowledgeTypeMethodology,
	KnowledgeTypeMemory, KnowledgeTypeAmmo, KnowledgeTypePlaybook,
}

// NormalizeKnowledgeType 归一 entry_type；未知值返回空串。
func NormalizeKnowledgeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	for _, v := range KnowledgeTypes {
		if t == v {
			return v
		}
	}
	return ""
}

// KnowledgeEntry 是 knowledge_entries 的一行。
type KnowledgeEntry struct {
	ID         int64  `json:"id"`
	Scope      string `json:"scope"`
	CompanyID  int64  `json:"company_id,omitempty"`
	TaskID     int64  `json:"task_id,omitempty"`
	EntryType  string `json:"entry_type"`
	Key        string `json:"key,omitempty"`
	Title      string `json:"title,omitempty"`
	Summary    string `json:"summary"`
	BodyMD     string `json:"body_md,omitempty"`
	MemoryKind string `json:"memory_kind,omitempty"`
	AmmoKind   string `json:"ammo_kind,omitempty"`
	// Fingerprint 只在 playbook 条目上有值（原始 JSONB）。
	Fingerprint    json.RawMessage `json:"fingerprint,omitempty"`
	FingerprintKey string          `json:"fingerprint_key,omitempty"`
	TechniqueKey   string          `json:"technique_key,omitempty"`
	Confidence     string          `json:"confidence,omitempty"`
	SourceType     string          `json:"source_type,omitempty"`
	SourceRef      string          `json:"source_ref,omitempty"`
	SourceTaskID   string          `json:"source_task_id,omitempty"`
	SourceNodeID   int64           `json:"source_node_id,omitempty"`
	EvidenceRef    string          `json:"evidence_ref,omitempty"`
	Hits           int             `json:"hits"`
	Uses           int             `json:"uses"`
	Successes      int             `json:"successes"`
	Failures       int             `json:"failures"`
	LastOutcome    string          `json:"last_outcome,omitempty"`
	LastNote       string          `json:"last_note,omitempty"`
	LastUsedAt     *time.Time      `json:"last_used_at,omitempty"`
	LastVerifiedAt *time.Time      `json:"last_verified_at,omitempty"`
	TTLDays        int             `json:"ttl_days,omitempty"`
	Status         string          `json:"status"`
	LegacyTable    string          `json:"legacy_table,omitempty"`
	LegacyID       int64           `json:"legacy_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// KnowledgeQuery 是检索条件；零值表示不限。
type KnowledgeQuery struct {
	Types     []string `json:"types,omitempty"`
	Scope     string   `json:"scope,omitempty"`
	CompanyID int64    `json:"company_id,omitempty"`
	TaskID    int64    `json:"task_id,omitempty"`
	// IncludeGlobal 为真时，company 查询会并上 global 条目（注入块用）。
	IncludeGlobal bool     `json:"include_global,omitempty"`
	Text          string   `json:"text,omitempty"`
	Status        []string `json:"status,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

const keCols = `id, scope, COALESCE(company_id,0), COALESCE(task_id,0), entry_type,
	key, title, summary, body_md, memory_kind, ammo_kind, fingerprint, fingerprint_key,
	technique_key, confidence, source_type, source_ref, source_task_id, source_node_id,
	evidence_ref, hits, uses, successes, failures, last_outcome, last_note,
	last_used_at, last_verified_at, ttl_days, status, legacy_table, legacy_id,
	created_at, updated_at`

func scanKnowledgeEntry(sc interface {
	Scan(dest ...any) error
}) (*KnowledgeEntry, error) {
	var e KnowledgeEntry
	var fp []byte
	var lastUsed, lastVerified sql.NullTime
	if err := sc.Scan(&e.ID, &e.Scope, &e.CompanyID, &e.TaskID, &e.EntryType,
		&e.Key, &e.Title, &e.Summary, &e.BodyMD, &e.MemoryKind, &e.AmmoKind, &fp, &e.FingerprintKey,
		&e.TechniqueKey, &e.Confidence, &e.SourceType, &e.SourceRef, &e.SourceTaskID, &e.SourceNodeID,
		&e.EvidenceRef, &e.Hits, &e.Uses, &e.Successes, &e.Failures, &e.LastOutcome, &e.LastNote,
		&lastUsed, &lastVerified, &e.TTLDays, &e.Status, &e.LegacyTable, &e.LegacyID,
		&e.CreatedAt, &e.UpdatedAt); err != nil {
		return nil, err
	}
	if len(fp) > 0 {
		e.Fingerprint = json.RawMessage(fp)
	}
	if lastUsed.Valid {
		e.LastUsedAt = &lastUsed.Time
	}
	if lastVerified.Valid {
		e.LastVerifiedAt = &lastVerified.Time
	}
	return &e, nil
}

// normalizeKnowledgeEntry 校验并归一写入值；返回错误时调用方不得落库。
func normalizeKnowledgeEntry(e *KnowledgeEntry) error {
	e.Scope = strings.ToLower(strings.TrimSpace(e.Scope))
	if e.Scope == "" {
		e.Scope = KnowledgeScopeCompany
	}
	rawType := e.EntryType
	e.EntryType = NormalizeKnowledgeType(e.EntryType)
	if e.EntryType == "" {
		return fmt.Errorf("未知知识类型 %q", rawType)
	}
	e.Summary = strings.TrimSpace(e.Summary)
	if e.Summary == "" {
		return fmt.Errorf("summary 不能为空")
	}
	switch e.Scope {
	case KnowledgeScopeCompany:
		if e.CompanyID <= 0 {
			return fmt.Errorf("scope=company 需要 company_id")
		}
		e.TaskID = 0
	case KnowledgeScopeGlobal:
		e.CompanyID, e.TaskID = 0, 0
	case KnowledgeScopeTask:
		if e.TaskID <= 0 {
			return fmt.Errorf("scope=task 需要 task_id")
		}
		e.CompanyID = 0
	default:
		return fmt.Errorf("未知 scope %q", e.Scope)
	}
	if e.Status == "" {
		e.Status = KnowledgeStatusActive
	}
	switch e.Status {
	case KnowledgeStatusActive, KnowledgeStatusStale, KnowledgeStatusRetired, KnowledgeStatusDeprecated:
	default:
		return fmt.Errorf("未知状态 %q", e.Status)
	}
	e.Confidence = NormalizeConfidence(e.Confidence)
	if e.TTLDays < 0 {
		e.TTLDays = 0
	}
	return nil
}

// InsertKnowledgeEntry 写入一条知识；legacy_* 用于回填去重（唯一索引）。
func (s *AssetStore) InsertKnowledgeEntry(e KnowledgeEntry) (*KnowledgeEntry, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	if err := normalizeKnowledgeEntry(&e); err != nil {
		return nil, err
	}
	var fp any
	if len(e.Fingerprint) > 0 {
		fp = []byte(e.Fingerprint)
	}
	row := s.db.QueryRow(`INSERT INTO knowledge_entries
		(scope, company_id, task_id, entry_type, key, title, summary, body_md, memory_kind, ammo_kind,
		 fingerprint, fingerprint_key, technique_key, confidence, source_type, source_ref,
		 source_task_id, source_node_id, evidence_ref, ttl_days, status, legacy_table, legacy_id)
		VALUES ($1,NULLIF($2,0),NULLIF($3,0),$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)
		RETURNING `+keCols,
		e.Scope, e.CompanyID, e.TaskID, e.EntryType, e.Key, e.Title, e.Summary, e.BodyMD,
		e.MemoryKind, e.AmmoKind, fp, e.FingerprintKey, e.TechniqueKey, e.Confidence,
		e.SourceType, e.SourceRef, e.SourceTaskID, e.SourceNodeID, e.EvidenceRef,
		e.TTLDays, e.Status, e.LegacyTable, e.LegacyID)
	return scanKnowledgeEntry(row)
}

// UpsertKnowledgePlaybook 按 (fingerprint_key, technique_key) 写入或更新打法卡。
// 返回 inserted=true 表示新建，false 表示更新已有卡片。
func (s *AssetStore) UpsertKnowledgePlaybook(e KnowledgeEntry) (*KnowledgeEntry, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("数据库未初始化")
	}
	e.Scope = KnowledgeScopeGlobal
	e.EntryType = KnowledgeTypePlaybook
	if err := normalizeKnowledgeEntry(&e); err != nil {
		return nil, false, err
	}
	if e.FingerprintKey == "" || e.TechniqueKey == "" {
		return nil, false, fmt.Errorf("打法卡需要 fingerprint_key 与 technique_key")
	}
	var fp any
	if len(e.Fingerprint) > 0 {
		fp = []byte(e.Fingerprint)
	}
	row := s.db.QueryRow(`INSERT INTO knowledge_entries
		(scope, entry_type, key, title, summary, body_md, fingerprint, fingerprint_key,
		 technique_key, confidence, source_task_id, source_node_id, evidence_ref, status)
		VALUES ('global','playbook',$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'active')
		ON CONFLICT (fingerprint_key, technique_key) WHERE entry_type='playbook' AND status IN ('active','stale')
		DO UPDATE SET title=EXCLUDED.title, summary=EXCLUDED.summary, body_md=EXCLUDED.body_md,
			fingerprint=EXCLUDED.fingerprint, confidence=EXCLUDED.confidence,
			evidence_ref=EXCLUDED.evidence_ref, status='active', updated_at=now()
		RETURNING `+keCols+`, (xmax = 0) AS inserted`,
		e.Key, e.Title, e.Summary, e.BodyMD, fp, e.FingerprintKey, e.TechniqueKey,
		e.Confidence, e.SourceTaskID, e.SourceNodeID, e.EvidenceRef)
	var inserted bool
	var out KnowledgeEntry
	var fpb []byte
	var lastUsed, lastVerified sql.NullTime
	if err := row.Scan(&out.ID, &out.Scope, &out.CompanyID, &out.TaskID, &out.EntryType,
		&out.Key, &out.Title, &out.Summary, &out.BodyMD, &out.MemoryKind, &out.AmmoKind, &fpb, &out.FingerprintKey,
		&out.TechniqueKey, &out.Confidence, &out.SourceType, &out.SourceRef, &out.SourceTaskID, &out.SourceNodeID,
		&out.EvidenceRef, &out.Hits, &out.Uses, &out.Successes, &out.Failures, &out.LastOutcome, &out.LastNote,
		&lastUsed, &lastVerified, &out.TTLDays, &out.Status, &out.LegacyTable, &out.LegacyID,
		&out.CreatedAt, &out.UpdatedAt, &inserted); err != nil {
		return nil, false, err
	}
	if len(fpb) > 0 {
		out.Fingerprint = json.RawMessage(fpb)
	}
	return &out, inserted, nil
}

// LookupKnowledge 按条件检索（文本用 ILIKE，便于中文；上限 100）。
func (s *AssetStore) LookupKnowledge(q KnowledgeQuery) ([]KnowledgeEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if len(q.Types) > 0 {
		add("entry_type = ANY($%d)", q.Types)
	}
	switch q.Scope {
	case KnowledgeScopeCompany:
		if q.CompanyID > 0 && q.IncludeGlobal {
			add("(company_id = $%d OR scope = 'global')", q.CompanyID)
		} else if q.CompanyID > 0 {
			add("company_id = $%d", q.CompanyID)
		}
	case KnowledgeScopeGlobal:
		where = append(where, "scope = 'global'")
	case KnowledgeScopeTask:
		if q.TaskID > 0 {
			add("task_id = $%d", q.TaskID)
		}
	}
	if len(q.Status) > 0 {
		add("status = ANY($%d)", q.Status)
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		args = append(args, "%"+t+"%")
		n := len(args)
		where = append(where, fmt.Sprintf(
			"(key ILIKE $%d OR title ILIKE $%d OR summary ILIKE $%d OR body_md ILIKE $%d)", n, n, n, n))
	}
	sqlText := `SELECT ` + keCols + ` FROM knowledge_entries`
	if len(where) > 0 {
		sqlText += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, limit)
	sqlText += fmt.Sprintf(` ORDER BY CASE status WHEN 'active' THEN 0 WHEN 'stale' THEN 1 ELSE 2 END,
		hits DESC, COALESCE(last_verified_at, created_at) DESC, id DESC LIMIT $%d`, len(args))
	rows, err := s.db.Query(sqlText, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []KnowledgeEntry{}
	for rows.Next() {
		e, err := scanKnowledgeEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// GetKnowledgeEntry 按 id 取一条。
func (s *AssetStore) GetKnowledgeEntry(id int64) (*KnowledgeEntry, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT `+keCols+` FROM knowledge_entries WHERE id=$1`, id)
	e, err := scanKnowledgeEntry(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return e, err
}

// TouchKnowledge 记录一次显式使用（只由 lookup_knowledge 调用，注入块不调用）。
func (s *AssetStore) TouchKnowledge(ids []int64) error {
	if s == nil || s.db == nil || len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(`UPDATE knowledge_entries SET hits=hits+1, last_used_at=now() WHERE id = ANY($1)`, ids)
	return err
}

// FeedbackKnowledge 记录一次结果（worked/failed/verified/stale）。
func (s *AssetStore) FeedbackKnowledge(id int64, outcome, note string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	outcome = strings.ToLower(strings.TrimSpace(outcome))
	var err error
	switch outcome {
	case "worked":
		_, err = s.db.Exec(`UPDATE knowledge_entries SET uses=uses+1, successes=successes+1,
			last_outcome='worked', last_note=$2, last_used_at=now(), last_verified_at=now() WHERE id=$1`, id, note)
	case "failed":
		_, err = s.db.Exec(`UPDATE knowledge_entries SET uses=uses+1, failures=failures+1,
			last_outcome='failed', last_note=$2, last_used_at=now() WHERE id=$1`, id, note)
	case "verified":
		_, err = s.db.Exec(`UPDATE knowledge_entries SET status='active', last_verified_at=now(),
			last_note=COALESCE(NULLIF($2,''), last_note) WHERE id=$1`, id, note)
	case "stale":
		_, err = s.db.Exec(`UPDATE knowledge_entries SET status='stale',
			last_note=COALESCE(NULLIF($2,''), last_note) WHERE id=$1`, id, note)
	default:
		return fmt.Errorf("outcome 只能是 worked/failed/verified/stale")
	}
	return err
}

// SetKnowledgeStatus 由人工/审批调用（active/stale/retired/deprecated）。
func (s *AssetStore) SetKnowledgeStatus(id int64, status, reason string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("数据库未初始化")
	}
	switch status {
	case KnowledgeStatusActive, KnowledgeStatusStale, KnowledgeStatusRetired, KnowledgeStatusDeprecated:
	default:
		return fmt.Errorf("未知状态 %q", status)
	}
	_, err := s.db.Exec(`UPDATE knowledge_entries SET status=$2,
		last_note=COALESCE(NULLIF($3,''), last_note) WHERE id=$1`, id, status, reason)
	return err
}

// KnowledgePlaybookMatch 是统一知识表里的打法卡 + 匹配分。
type KnowledgePlaybookMatch struct {
	KnowledgeEntry
	Score int `json:"score"`
}

func knowledgeToPlaybook(e KnowledgeEntry) Playbook {
	p := Playbook{
		ID: e.ID, FingerprintKey: e.FingerprintKey, TechniqueKey: e.TechniqueKey,
		Title: e.Title, Summary: e.Summary, Steps: e.BodyMD, Evidence: e.EvidenceRef,
		Confidence: e.Confidence, SourceTaskID: e.SourceTaskID, SourceNodeID: e.SourceNodeID,
		Status: e.Status, Uses: e.Uses, Successes: e.Successes, Failures: e.Failures,
		LastOutcome: e.LastOutcome, LastNote: e.LastNote,
		LastUsedAt: e.LastUsedAt, LastVerifiedAt: e.LastVerifiedAt,
		CreatedAt: e.CreatedAt, UpdatedAt: e.UpdatedAt,
	}
	if len(e.Fingerprint) > 0 {
		_ = json.Unmarshal(e.Fingerprint, &p.Fingerprint)
	}
	return p
}

// MatchKnowledgePlaybooks 用指纹在统一知识表里匹配打法卡（lookup_knowledge 的指纹模式）。
func (s *AssetStore) MatchKnowledgePlaybooks(fp PlaybookFingerprint, limit int) ([]KnowledgePlaybookMatch, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 3
	}
	cards, err := s.LookupKnowledge(KnowledgeQuery{
		Types:  []string{KnowledgeTypePlaybook},
		Status: []string{KnowledgeStatusActive, KnowledgeStatusStale},
		Limit:  200,
	})
	if err != nil {
		return nil, err
	}
	sig := signalsFromFingerprint(fp.Normalize())
	out := make([]KnowledgePlaybookMatch, 0, 3)
	for _, e := range cards {
		p := knowledgeToPlaybook(e)
		if score := scorePlaybook(&p, sig); score > 0 {
			out = append(out, KnowledgePlaybookMatch{KnowledgeEntry: e, Score: score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		ri := playbookSuccessRate(&Playbook{Successes: out[i].Successes, Failures: out[i].Failures})
		rj := playbookSuccessRate(&Playbook{Successes: out[j].Successes, Failures: out[j].Failures})
		if ri != rj {
			return ri > rj
		}
		return out[i].UpdatedAt.After(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// MatchKnowledgePlaybooksForTask 用任务范围内资产的指纹信号匹配打法卡（注入块用）。
func (s *AssetStore) MatchKnowledgePlaybooksForTask(taskID int64, limit int) ([]KnowledgePlaybookMatch, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return nil, nil
	}
	tf := s.TaskFingerprint(taskID)
	if len(tf.Technologies) == 0 && len(tf.Favicons) == 0 && len(tf.Services) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 2
	}
	cards, err := s.LookupKnowledge(KnowledgeQuery{
		Types:  []string{KnowledgeTypePlaybook},
		Status: []string{KnowledgeStatusActive, KnowledgeStatusStale},
		Limit:  200,
	})
	if err != nil {
		return nil, err
	}
	sig := signalsFromTask(tf)
	out := make([]KnowledgePlaybookMatch, 0, 2)
	for _, e := range cards {
		p := knowledgeToPlaybook(e)
		if score := scorePlaybook(&p, sig); score > 0 {
			out = append(out, KnowledgePlaybookMatch{KnowledgeEntry: e, Score: score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// KnowledgeBlock 渲染注入用的跨任务知识块（预算 3000 字符，超预算截断并给检索提示）。
// 顺序：本任务弹药 → 公司记忆 → 全局记忆 → 公司知识 → 同类打法。
// 注入**不**自增 hits（保护 system 前缀缓存）；只有显式 lookup_knowledge 才计数。
func (s *AssetStore) KnowledgeBlock(taskID int64, planner bool, budget int) (string, error) {
	if s == nil || s.db == nil || taskID <= 0 {
		return "", nil
	}
	if budget <= 0 {
		budget = 3000
	}
	companyIDs := s.knowledgeTaskCompanyIDs(taskID)
	var b strings.Builder
	budgetLeft := func() bool { return b.Len() < budget }
	writeLine := func(format string, args ...any) bool {
		if !budgetLeft() {
			return false
		}
		fmt.Fprintf(&b, format, args...)
		return budgetLeft()
	}
	clip := func(v string, n int) string {
		r := []rune(strings.TrimSpace(v))
		if len(r) > n {
			return string(r[:n]) + "…"
		}
		return string(r)
	}

	// 1) 本任务弹药（scope=task）。
	ammo, err := s.LookupKnowledge(KnowledgeQuery{Scope: KnowledgeScopeTask, TaskID: taskID,
		Types: []string{KnowledgeTypeAmmo}, Status: []string{KnowledgeStatusActive}, Limit: 12})
	if err != nil {
		return "", err
	}
	if len(ammo) > 0 {
		writeLine("· 本任务弹药（复用这些，不要重复发现）\n")
		for _, e := range ammo {
			if !writeLine("  - [%s] %s\n", firstNonEmpty(e.AmmoKind, e.EntryType), clip(e.Summary, 120)) {
				break
			}
		}
	}

	// 2) 公司记忆（按热度与复核时间排序）。
	memTypes := []string{KnowledgeTypeMemory, KnowledgeTypeVendorRule, KnowledgeTypeMethodology}
	for _, cid := range companyIDs {
		mems, err := s.LookupKnowledge(KnowledgeQuery{Scope: KnowledgeScopeCompany, CompanyID: cid,
			Types: memTypes, Status: []string{KnowledgeStatusActive, KnowledgeStatusStale}, Limit: 6})
		if err != nil {
			return "", err
		}
		if len(mems) == 0 {
			continue
		}
		writeLine("· 厂商记忆 · 公司#%d\n", cid)
		for _, e := range mems {
			stale := e.LastVerifiedAt == nil || time.Since(*e.LastVerifiedAt) > 60*24*time.Hour || e.Status == KnowledgeStatusStale
			flag := ""
			if stale {
				flag = " ⚠未复核"
			}
			if !writeLine("  - [#%d] %s：%s（%s%s）\n", e.ID, firstNonEmpty(e.Key, e.Title),
				clip(e.Summary, 200), firstNonEmpty(e.Confidence, "unknown"), flag) {
				break
			}
		}
	}

	// 3) 全局记忆。
	if budgetLeft() {
		gm, err := s.LookupKnowledge(KnowledgeQuery{Scope: KnowledgeScopeGlobal,
			Types: memTypes, Status: []string{KnowledgeStatusActive}, Limit: 4})
		if err != nil {
			return "", err
		}
		if len(gm) > 0 {
			writeLine("· 全局记忆\n")
			for _, e := range gm {
				if !writeLine("  - [#%d] %s：%s\n", e.ID, firstNonEmpty(e.Key, e.Title), clip(e.Summary, 160)) {
					break
				}
			}
		}
	}

	// 4) 公司知识（fact/conclusion/asset_hint/playbook_hint，最多 5 行）。
	for _, cid := range companyIDs {
		notes, err := s.LookupKnowledge(KnowledgeQuery{Scope: KnowledgeScopeCompany, CompanyID: cid,
			Types:  []string{KnowledgeTypeFact, KnowledgeTypeConclusion, KnowledgeTypeAssetHint, KnowledgeTypePlaybookHint},
			Status: []string{KnowledgeStatusActive}, Limit: 5})
		if err != nil {
			return "", err
		}
		if len(notes) == 0 {
			continue
		}
		writeLine("· 公司已知\n")
		for _, e := range notes {
			if !writeLine("  - [%s] %s\n", e.EntryType, clip(e.Summary, 160)) {
				break
			}
		}
	}

	// 5) 同类打法（按任务指纹匹配，只给指针）。
	if budgetLeft() {
		matches, err := s.MatchKnowledgePlaybooksForTask(taskID, 2)
		if err != nil {
			return "", err
		}
		for _, m := range matches {
			stats := "尚无实战反馈"
			if total := m.Successes + m.Failures; total > 0 {
				stats = fmt.Sprintf("成功 %d/%d", m.Successes, total)
			}
			if !writeLine("· 同类打法 [#%d] %s（匹配 %d · %s）——完整步骤用 lookup_knowledge(asset_id=...) 取\n",
				m.ID, clip(firstNonEmpty(m.Title, m.Summary), 80), m.Score, stats) {
				break
			}
		}
	}

	if b.Len() == 0 {
		return "", nil
	}
	head := "【跨任务知识（公司/全局沉淀，按热度与近期复核排序；标 ⚠ 的长期未复核，采信前先验证）】\n"
	tail := "（预算 %d 字；更多用 lookup_knowledge 检索；与现实冲突用 knowledge_feedback(id, outcome=stale) 纠错）\n"
	out := head + b.String()
	if !budgetLeft() {
		out += "（已达注入预算，其余用 lookup_knowledge 检索）\n"
	}
	out += fmt.Sprintf(tail, budget)
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// knowledgeTaskCompanyIDs 解析任务关联公司：显式 company scope 优先，其次复用
// CompanyStore 的三段式归因（域名后缀 / 任务文本）。
func (s *AssetStore) knowledgeTaskCompanyIDs(taskID int64) []int64 {
	seen := map[int64]bool{}
	out := []int64{}
	if s.db != nil {
		rows, err := s.db.Query(`SELECT DISTINCT company_id FROM task_scope
			WHERE task_id=$1 AND kind='company' AND company_id IS NOT NULL`, taskID)
		if err == nil {
			for rows.Next() {
				var id int64
				if rows.Scan(&id) == nil && !seen[id] {
					seen[id] = true
					out = append(out, id)
				}
			}
			rows.Close()
		}
	}
	if len(out) == 0 && s.company != nil {
		if profs, err := s.company.ResolveTaskCompanies(taskID); err == nil {
			for _, p := range profs {
				if !seen[p.CompanyID] {
					seen[p.CompanyID] = true
					out = append(out, p.CompanyID)
				}
			}
		}
	}
	return out
}
