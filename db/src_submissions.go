package db

// SRC 交付面（P2）：发现元数据（src_finding_meta）+ 提交台账（src_submissions）。
//
// 口径：
//   - 台账的粒度是"一条可提交件"（finding × platform），不是"一个任务"；
//   - report_ready 是**判定结果**，不是人工勾的标记：证据合规 + 正文齐备才为真；
//   - 指纹只由漏洞类型 + 归一化端点算（见 report.Fingerprint），没有端点就不算指纹，
//     也就不会参与跨任务去重——宁可少报"疑似重复"，不可误导人合并。

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// 台账状态（与 schema.sql 的 CHECK 一致）。
const (
	SrcSubDraft     = "draft"
	SrcSubReady     = "ready"
	SrcSubSubmitted = "submitted"
	SrcSubAccepted  = "accepted"
	SrcSubDuplicate = "duplicate"
	SrcSubRejected  = "rejected"
	SrcSubPaid      = "paid"
	SrcSubWithdrawn = "withdrawn"
)

// SrcSubmissionStatuses 是全部合法状态（顺序即 UI 展示顺序）。
var SrcSubmissionStatuses = []string{
	SrcSubDraft, SrcSubReady, SrcSubSubmitted, SrcSubAccepted,
	SrcSubDuplicate, SrcSubRejected, SrcSubPaid, SrcSubWithdrawn,
}

// srcSubmissionTransitions 是状态机：draft → ready → submitted → accepted|duplicate|rejected；
// accepted → paid；任意 → withdrawn。**不做**"退回 draft"——退回等于抹掉已经发生过的事。
var srcSubmissionTransitions = map[string][]string{
	SrcSubDraft:     {SrcSubReady, SrcSubWithdrawn},
	SrcSubReady:     {SrcSubSubmitted, SrcSubWithdrawn, SrcSubDraft},
	SrcSubSubmitted: {SrcSubAccepted, SrcSubDuplicate, SrcSubRejected, SrcSubWithdrawn},
	SrcSubAccepted:  {SrcSubPaid, SrcSubWithdrawn},
	SrcSubDuplicate: {SrcSubWithdrawn},
	SrcSubRejected:  {SrcSubWithdrawn},
	SrcSubPaid:      {},
	SrcSubWithdrawn: {},
}

// 平台驳回原因码：封闭词表，供回注提示词与打法库反馈使用。
const (
	SrcVerdictNotReproducible  = "not_reproducible"
	SrcVerdictDuplicate        = "duplicate"
	SrcVerdictInsufficient     = "insufficient_info"
	SrcVerdictNotVulnerability = "not_vulnerability"
	SrcVerdictOutOfScope       = "out_of_scope"
	SrcVerdictOther            = "other"
)

var SrcVerdictReasons = []string{
	SrcVerdictNotReproducible, SrcVerdictDuplicate, SrcVerdictInsufficient,
	SrcVerdictNotVulnerability, SrcVerdictOutOfScope, SrcVerdictOther,
}

func IsSrcVerdictReason(s string) bool {
	s = strings.TrimSpace(s)
	for _, v := range SrcVerdictReasons {
		if v == s {
			return true
		}
	}
	return false
}

// IsSrcSubmissionStatus reports whether s is a legal ledger status.
func IsSrcSubmissionStatus(s string) bool {
	for _, v := range SrcSubmissionStatuses {
		if v == s {
			return true
		}
	}
	return false
}

// CanTransitionSrcSubmission checks one state-machine edge. Empty `from` means the
// row does not exist yet, so any status is acceptable.
func CanTransitionSrcSubmission(from, to string) error {
	if !IsSrcSubmissionStatus(to) {
		return fmt.Errorf("未知台账状态：%s", to)
	}
	if from == "" || from == to {
		return nil
	}
	for _, next := range srcSubmissionTransitions[from] {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("台账状态不能从 %s 直接变为 %s", from, to)
}

// SrcSubmission is one row of the submission ledger.
type SrcSubmission struct {
	ID            int64      `json:"id"`
	TaskID        int64      `json:"task_id"`
	FindingID     int64      `json:"finding_id"`
	Platform      string     `json:"platform"`
	ExternalID    string     `json:"external_id"`
	Title         string     `json:"title"`
	Severity      string     `json:"severity"`
	PlatformLevel string     `json:"platform_level"`
	Status        string     `json:"status"`
	Fingerprint   string     `json:"fingerprint"`
	Description   string     `json:"description,omitempty"`
	Impact        string     `json:"impact,omitempty"`
	Fix           string     `json:"fix,omitempty"`
	ReportMD      string     `json:"report_md,omitempty"`
	SubmittedAt   *time.Time `json:"submitted_at,omitempty"`
	VerdictAt     *time.Time `json:"verdict_at,omitempty"`
	Reward        string     `json:"reward"`
	Note          string     `json:"note"`
	// VerdictReason 是平台驳回原因码（见 SrcVerdictReasons）；空 = 未记录。
	VerdictReason string    `json:"verdict_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// SrcFindingMeta is the delivery-side metadata of one finding.
type SrcFindingMeta struct {
	FindingID   int64  `json:"finding_id"`
	TaskID      int64  `json:"task_id"`
	Fingerprint string `json:"fingerprint"`
	// DegradedFingerprint 是没有绑定流量时的低置信指纹（d1: 前缀），只用于疑似重复
	// 提示，不自动定案。
	DegradedFingerprint string          `json:"degraded_fingerprint,omitempty"`
	Compliance          json.RawMessage `json:"compliance"`
	ReportReady         bool            `json:"report_ready"`
	DedupeOf            int64           `json:"dedupe_of,omitempty"`
	UpdatedAt           time.Time       `json:"updated_at"`
}

// SrcDeliveryFinding 是收尾门判"能不能交付"用的一行：发现本身 + 台账 + 元数据。
type SrcDeliveryFinding struct {
	FindingID        int64  `json:"finding_id"`
	TaskID           int64  `json:"task_id"`
	Severity         string `json:"severity"`
	Status           string `json:"status"`
	Title            string `json:"title"`
	Fingerprint      string `json:"fingerprint"`
	ReportReady      bool   `json:"report_ready"`
	DedupeOf         int64  `json:"dedupe_of,omitempty"`
	SubmissionID     int64  `json:"submission_id,omitempty"`
	SubmissionStatus string `json:"submission_status,omitempty"`
}

// SrcDuplicate 是跨任务查重命中一条（可能是别的任务里同一个洞）。
type SrcDuplicate struct {
	TaskID        int64  `json:"task_id"`
	TaskName      string `json:"task_name,omitempty"`
	FindingID     int64  `json:"finding_id"`
	Title         string `json:"title"`
	Severity      string `json:"severity"`
	Status        string `json:"status"`
	Platform      string `json:"platform,omitempty"`
	ExternalID    string `json:"external_id,omitempty"`
	SubmissionSt  string `json:"submission_status,omitempty"`
	CreatedAtText string `json:"created_at,omitempty"`
}

const srcSubmissionCols = `id, task_id, finding_id, platform, external_id, title, severity,
COALESCE(platform_level,''), status, fingerprint, description, impact, fix, report_md,
submitted_at, verdict_at, reward, note, COALESCE(verdict_reason,''), created_at, updated_at`

func scanSrcSubmission(sc interface{ Scan(...any) error }) (*SrcSubmission, error) {
	var s SrcSubmission
	if err := sc.Scan(&s.ID, &s.TaskID, &s.FindingID, &s.Platform, &s.ExternalID, &s.Title, &s.Severity,
		&s.PlatformLevel, &s.Status, &s.Fingerprint, &s.Description, &s.Impact, &s.Fix, &s.ReportMD,
		&s.SubmittedAt, &s.VerdictAt, &s.Reward, &s.Note, &s.VerdictReason, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// SrcSubmissionInput is what a caller must decide before the ledger row is written.
// ReportReady/Compliance are judgments made by the caller (srcmode), not user input.
type SrcSubmissionInput struct {
	TaskID      int64
	FindingID   int64
	Platform    string
	Title       string
	Severity    string
	Fingerprint string
	// DegradedFingerprint 见 SrcFindingMeta；空 = 没有可用的降级指纹。
	DegradedFingerprint string
	Description         string
	Impact              string
	Fix                 string
	ReportMD            string
	Status              string // "" = draft
	ReportReady         bool
	Compliance          json.RawMessage
	// P3：绑定的平台项目 + 该项目等级映射后的平台等级（没有映射时为空）。
	ProgramID     int64
	PlatformLevel string
}

// PrepareSrcSubmission 写（或更新）一条台账 + 它的元数据，并把报告是否齐备记到
// src_finding_meta。已经 submit 过的行不会被"重新准备"打回 draft——那会抹掉台账。
func (s *SrcStore) PrepareSrcSubmission(ctx context.Context, in SrcSubmissionInput) (*SrcSubmission, *SrcFindingMeta, error) {
	if s == nil || s.db == nil {
		return nil, nil, fmt.Errorf("src store unavailable")
	}
	if in.FindingID <= 0 || in.TaskID <= 0 {
		return nil, nil, fmt.Errorf("台账需要 task_id 与 finding_id")
	}
	status := strings.TrimSpace(in.Status)
	if status == "" {
		status = SrcSubDraft
	}
	if !IsSrcSubmissionStatus(status) {
		return nil, nil, fmt.Errorf("未知台账状态：%s", status)
	}
	platform := strings.TrimSpace(in.Platform)
	compliance := in.Compliance
	if len(compliance) == 0 {
		compliance = json.RawMessage(`{}`)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()

	var rowID int64
	var programID any
	if in.ProgramID > 0 {
		programID = in.ProgramID
	}
	err = tx.QueryRowContext(ctx, `
INSERT INTO src_submissions(task_id, finding_id, platform, title, severity, status, fingerprint,
                            description, impact, fix, report_md, program_id, platform_level)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
ON CONFLICT (finding_id, platform) DO UPDATE SET
    title          = EXCLUDED.title,
    severity       = EXCLUDED.severity,
    fingerprint    = EXCLUDED.fingerprint,
    description    = EXCLUDED.description,
    impact         = EXCLUDED.impact,
    fix            = EXCLUDED.fix,
    report_md      = EXCLUDED.report_md,
    program_id     = COALESCE(EXCLUDED.program_id, src_submissions.program_id),
    platform_level = COALESCE(NULLIF(EXCLUDED.platform_level,''), src_submissions.platform_level),
    -- 已交出去的行保持它自己的状态：重写正文不该把它拉回 draft。
    status         = CASE WHEN src_submissions.status IN ('submitted','accepted','duplicate','rejected','paid','withdrawn')
                          THEN src_submissions.status ELSE EXCLUDED.status END
RETURNING id`,
		in.TaskID, in.FindingID, platform, in.Title, in.Severity, status, in.Fingerprint,
		in.Description, in.Impact, in.Fix, in.ReportMD, programID, in.PlatformLevel).Scan(&rowID)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO src_finding_meta(finding_id, task_id, fingerprint, degraded_fingerprint, compliance, report_ready)
VALUES ($1,$2,$3,$4,$5,$6)
ON CONFLICT (finding_id) DO UPDATE SET
    task_id              = EXCLUDED.task_id,
    fingerprint          = EXCLUDED.fingerprint,
    degraded_fingerprint = EXCLUDED.degraded_fingerprint,
    compliance           = EXCLUDED.compliance,
    report_ready         = EXCLUDED.report_ready,
    updated_at           = now()`,
		in.FindingID, in.TaskID, in.Fingerprint, in.DegradedFingerprint, string(compliance), in.ReportReady); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	sub, err := s.GetSrcSubmission(ctx, rowID)
	if err != nil {
		return nil, nil, err
	}
	meta, err := s.GetSrcFindingMeta(ctx, in.FindingID)
	if err != nil {
		return nil, nil, err
	}
	return sub, meta, nil
}

// GetSrcSubmission returns one ledger row (nil when missing).
func (s *SrcStore) GetSrcSubmission(ctx context.Context, id int64) (*SrcSubmission, error) {
	if s == nil || s.db == nil || id <= 0 {
		return nil, nil
	}
	sub, err := scanSrcSubmission(s.db.QueryRowContext(ctx,
		`SELECT `+srcSubmissionCols+` FROM src_submissions WHERE id=$1`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// GetSrcSubmissionByFinding returns the ledger row of one finding on one platform
// ("" = the default platform, i.e. the row a task's own agent usually writes).
func (s *SrcStore) GetSrcSubmissionByFinding(ctx context.Context, findingID int64, platform string) (*SrcSubmission, error) {
	if s == nil || s.db == nil || findingID <= 0 {
		return nil, nil
	}
	sub, err := scanSrcSubmission(s.db.QueryRowContext(ctx,
		`SELECT `+srcSubmissionCols+` FROM src_submissions WHERE finding_id=$1 AND platform=$2`,
		findingID, strings.TrimSpace(platform)))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// SrcSubmissionFilter selects ledger rows (all fields optional).
type SrcSubmissionFilter struct {
	TaskID   int64  `json:"task_id,omitempty"`
	Status   string `json:"status,omitempty"`
	Platform string `json:"platform,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

// ListSrcSubmissions lists ledger rows, newest first, bounded by Limit (default 200).
func (s *SrcStore) ListSrcSubmissions(ctx context.Context, f SrcSubmissionFilter) ([]SrcSubmission, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	query := `SELECT ` + srcSubmissionCols + ` FROM src_submissions WHERE 1=1`
	args := []any{}
	if f.TaskID > 0 {
		args = append(args, f.TaskID)
		query += fmt.Sprintf(" AND task_id=$%d", len(args))
	}
	if st := strings.TrimSpace(f.Status); st != "" {
		args = append(args, st)
		query += fmt.Sprintf(" AND status=$%d", len(args))
	}
	if p := strings.TrimSpace(f.Platform); p != "" {
		args = append(args, p)
		query += fmt.Sprintf(" AND platform=$%d", len(args))
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY updated_at DESC, id DESC LIMIT $%d", len(args))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcSubmission{}
	for rows.Next() {
		sub, err := scanSrcSubmission(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sub)
	}
	return out, rows.Err()
}

// SrcSubmissionPatch is a partial ledger update (nil = leave unchanged).
type SrcSubmissionPatch struct {
	Status        *string `json:"status,omitempty"`
	ExternalID    *string `json:"external_id,omitempty"`
	PlatformLevel *string `json:"platform_level,omitempty"`
	Reward        *string `json:"reward,omitempty"`
	Note          *string `json:"note,omitempty"`
	VerdictReason *string `json:"verdict_reason,omitempty"`
}

// UpdateSrcSubmission applies a partial update, validating the state machine and
// stamping submitted_at/verdict_at at the transitions that need them.
func (s *SrcStore) UpdateSrcSubmission(ctx context.Context, id int64, patch SrcSubmissionPatch) (*SrcSubmission, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("src store unavailable")
	}
	cur, err := s.GetSrcSubmission(ctx, id)
	if err != nil {
		return nil, err
	}
	if cur == nil {
		return nil, fmt.Errorf("台账 #%d 不存在", id)
	}
	nextStatus := cur.Status
	if patch.Status != nil {
		nextStatus = strings.TrimSpace(*patch.Status)
		if err := CanTransitionSrcSubmission(cur.Status, nextStatus); err != nil {
			return nil, err
		}
	}
	// submitted 必须有平台编号或提交凭证：没有凭证的"已提交"只是自我安慰。
	externalID := cur.ExternalID
	if patch.ExternalID != nil {
		externalID = strings.TrimSpace(*patch.ExternalID)
	}
	if nextStatus == SrcSubSubmitted && externalID == "" {
		return nil, fmt.Errorf("标记为 submitted 必须填 external_id（平台编号或提交凭证）")
	}
	// duplicate 必须指向重复对象，避免用状态掩盖重复确认这一步。
	if nextStatus == SrcSubDuplicate {
		meta, err := s.GetSrcFindingMeta(ctx, cur.FindingID)
		if err != nil {
			return nil, err
		}
		if meta == nil || meta.DedupeOf == 0 {
			return nil, fmt.Errorf("标记为 duplicate 前必须先登记 dedupe_of（它和哪条是同一個洞）")
		}
	}
	// ready 是"可交付"的承诺，submitted 是"已经交出去"：两者都必须建立在真实的
	// 交付校验之上。否则 PATCH 一条 draft→ready 就能把收尾门的交付缺口清掉。
	if nextStatus == SrcSubReady || nextStatus == SrcSubSubmitted {
		if err := s.requireSubmittable(ctx, cur.FindingID); err != nil {
			return nil, err
		}
	}
	platformLevel := cur.PlatformLevel
	if patch.PlatformLevel != nil {
		platformLevel = strings.TrimSpace(*patch.PlatformLevel)
	}
	reward := cur.Reward
	if patch.Reward != nil {
		reward = strings.TrimSpace(*patch.Reward)
	}
	note := cur.Note
	if patch.Note != nil {
		note = strings.TrimSpace(*patch.Note)
	}
	verdictReason := cur.VerdictReason
	if patch.VerdictReason != nil {
		verdictReason = strings.TrimSpace(*patch.VerdictReason)
		if verdictReason != "" && !IsSrcVerdictReason(verdictReason) {
			return nil, fmt.Errorf("未知驳回原因码：%s（可选：%s）", verdictReason, strings.Join(SrcVerdictReasons, " / "))
		}
	}
	var submittedAt, verdictAt *time.Time
	if nextStatus == SrcSubSubmitted && cur.SubmittedAt == nil {
		now := time.Now()
		submittedAt = &now
	}
	if (nextStatus == SrcSubAccepted || nextStatus == SrcSubRejected || nextStatus == SrcSubDuplicate) && cur.VerdictAt == nil {
		now := time.Now()
		verdictAt = &now
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE src_submissions SET
    status         = $2,
    external_id    = $3,
    platform_level = $4,
    reward         = $5,
    note           = $6,
    verdict_reason = $7,
    submitted_at   = COALESCE($8, submitted_at),
    verdict_at     = COALESCE($9, verdict_at)
WHERE id=$1`, id, nextStatus, externalID, platformLevel, reward, note, verdictReason, submittedAt, verdictAt)
	if err != nil {
		return nil, err
	}
	return s.GetSrcSubmission(ctx, id)
}

// requireSubmittable 是台账推进到 ready/submitted 前的硬校验：可提交件必须真的过了
// 交付校验（report_ready），且（有合规记录时）compliance.ok=true。人工想要放行未过校验
// 的件，正确做法是先补正文/证据并重新准备，而不是改台账状态。
func (s *SrcStore) requireSubmittable(ctx context.Context, findingID int64) error {
	meta, err := s.GetSrcFindingMeta(ctx, findingID)
	if err != nil {
		return err
	}
	if meta == nil || !meta.ReportReady {
		return fmt.Errorf("发现 #%d 的可提交件还没通过交付校验（report_ready=false）：先补正文/证据并重新 src_prepare_submission", findingID)
	}
	if len(meta.Compliance) > 0 {
		var c struct {
			OK bool `json:"ok"`
		}
		if err := json.Unmarshal(meta.Compliance, &c); err != nil {
			return fmt.Errorf("发现 #%d 的合规记录无法解析：%v", findingID, err)
		}
		if !c.OK {
			return fmt.Errorf("发现 #%d 的证据合规未通过（compliance.ok=false）：先补齐证据再推进台账", findingID)
		}
	}
	return nil
}

// GetSrcFindingMeta returns one finding's delivery metadata (nil when missing).
func (s *SrcStore) GetSrcFindingMeta(ctx context.Context, findingID int64) (*SrcFindingMeta, error) {
	if s == nil || s.db == nil || findingID <= 0 {
		return nil, nil
	}
	var m SrcFindingMeta
	var dedupe sql.NullInt64
	var compliance []byte
	err := s.db.QueryRowContext(ctx, `
SELECT finding_id, COALESCE(task_id,0), fingerprint, degraded_fingerprint, compliance, report_ready, dedupe_of, updated_at
FROM src_finding_meta WHERE finding_id=$1`, findingID).
		Scan(&m.FindingID, &m.TaskID, &m.Fingerprint, &m.DegradedFingerprint, &compliance, &m.ReportReady, &dedupe, &m.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	m.Compliance = json.RawMessage(compliance)
	if dedupe.Valid {
		m.DedupeOf = dedupe.Int64
	}
	return &m, nil
}

// SrcSuspectsByDegradedFingerprint 查"低置信相似"的发现：没有绑定流量时用类型 +
// host/路径 + 参数名算出的降级指纹。命中只作提示，不写 dedupe_of、不阻断交付。
func (s *SrcStore) SrcSuspectsByDegradedFingerprint(ctx context.Context, fingerprint string, excludeFindingID int64, limit int) ([]SrcDuplicate, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if s == nil || s.db == nil || fingerprint == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT f.task_id, COALESCE(t.name,''), f.id, COALESCE(NULLIF(f.name,''), f.vulnclass),
       f.severity, f.status, COALESCE(s.platform,''), COALESCE(s.external_id,''),
       COALESCE(s.status,''), to_char(f.created_at,'YYYY-MM-DD HH24:MI')
FROM src_finding_meta m
JOIN findings f ON f.id = m.finding_id
LEFT JOIN tasks t ON t.id = f.task_id
LEFT JOIN src_submissions s ON s.finding_id = f.id AND s.platform = ''
WHERE m.degraded_fingerprint = $1 AND f.id <> $2
ORDER BY f.id DESC LIMIT $3`, fingerprint, excludeFindingID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcDuplicate{}
	for rows.Next() {
		var d SrcDuplicate
		if err := rows.Scan(&d.TaskID, &d.TaskName, &d.FindingID, &d.Title, &d.Severity,
			&d.Status, &d.Platform, &d.ExternalID, &d.SubmissionSt, &d.CreatedAtText); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetSrcDedupeOf records "this finding looks like that one" (0 clears it).
func (s *SrcStore) SetSrcDedupeOf(ctx context.Context, findingID, of int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("src store unavailable")
	}
	var arg any
	if of > 0 {
		arg = of
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO src_finding_meta(finding_id, task_id, dedupe_of)
SELECT f.id, COALESCE(f.task_id,0), $2 FROM findings f WHERE f.id=$1
ON CONFLICT (finding_id) DO UPDATE SET dedupe_of=EXCLUDED.dedupe_of, updated_at=now()`,
		findingID, arg)
	return err
}

// SrcDeliveryFindings returns every finding of a task with its ledger + metadata
// state — the收尾门 uses this to decide whether the task can be delivered at all.
func (s *SrcStore) SrcDeliveryFindings(ctx context.Context, taskID int64) ([]SrcDeliveryFinding, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT f.id, COALESCE(f.task_id,0), f.severity, f.status, COALESCE(NULLIF(f.name,''), f.vulnclass),
       COALESCE(m.fingerprint,''), COALESCE(m.report_ready,false), COALESCE(m.dedupe_of,0),
       COALESCE(s.id,0), COALESCE(s.status,'')
FROM findings f
LEFT JOIN src_finding_meta m ON m.finding_id = f.id
LEFT JOIN src_submissions  s ON s.finding_id = f.id AND s.platform = ''
WHERE f.task_id = $1
ORDER BY f.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcDeliveryFinding{}
	for rows.Next() {
		var f SrcDeliveryFinding
		var dedupe sql.NullInt64
		if err := rows.Scan(&f.FindingID, &f.TaskID, &f.Severity, &f.Status, &f.Title,
			&f.Fingerprint, &f.ReportReady, &dedupe, &f.SubmissionID, &f.SubmissionStatus); err != nil {
			return nil, err
		}
		if dedupe.Valid {
			f.DedupeOf = dedupe.Int64
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SrcDuplicatesByFingerprint finds submissions/findings sharing a fingerprint across
// tasks. The same-task, same-finding row is excluded: dedupe 是给"跨任务/跨项目"用的。
func (s *SrcStore) SrcDuplicatesByFingerprint(ctx context.Context, fingerprint string, excludeFindingID int64, limit int) ([]SrcDuplicate, error) {
	fingerprint = strings.TrimSpace(fingerprint)
	if s == nil || s.db == nil || fingerprint == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, `
SELECT f.task_id, COALESCE(t.name,''), f.id, COALESCE(NULLIF(f.name,''), f.vulnclass),
       f.severity, f.status, COALESCE(s.platform,''), COALESCE(s.external_id,''),
       COALESCE(s.status,''), to_char(f.created_at,'YYYY-MM-DD HH24:MI')
FROM src_finding_meta m
JOIN findings f ON f.id = m.finding_id
LEFT JOIN tasks t ON t.id = f.task_id
LEFT JOIN src_submissions s ON s.finding_id = f.id AND s.platform = ''
WHERE m.fingerprint = $1 AND f.id <> $2
ORDER BY f.id DESC LIMIT $3`, fingerprint, excludeFindingID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SrcDuplicate{}
	for rows.Next() {
		var d SrcDuplicate
		if err := rows.Scan(&d.TaskID, &d.TaskName, &d.FindingID, &d.Title, &d.Severity, &d.Status,
			&d.Platform, &d.ExternalID, &d.SubmissionSt, &d.CreatedAtText); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// SrcHostInScope 判断一个 host 是否落在任务测试范围内（root_domain 支持子域后缀，
// ip/cidr 走网段包含）。company/icp/keyword 类 scope 无法从一个 host 反推，返回
// (false,false) 表示"判定不了"——调用方据此**不要**把它当成越界。
func (s *SrcStore) SrcHostInScope(ctx context.Context, taskID int64, host string) (inScope bool, decidable bool, err error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if s == nil || s.db == nil || taskID <= 0 || host == "" {
		return false, false, nil
	}
	var root, sub, net int
	err = s.db.QueryRowContext(ctx, `
SELECT
  count(*) FILTER (WHERE kind='root_domain' AND ($2 = domain OR $2 LIKE '%.' || domain)),
  count(*) FILTER (WHERE kind='subdomain' AND $2 = domain),
  count(*) FILTER (WHERE kind IN ('ip','cidr') AND ts.net >>= try_inet($2))
FROM task_scope ts WHERE ts.task_id=$1`, taskID, host).Scan(&root, &sub, &net)
	if err != nil {
		return false, false, err
	}
	if root+sub+net > 0 {
		return true, true, nil
	}
	// 有没有"能判定的 scope"？没有就别下结论。
	var decidableKinds int
	if err := s.db.QueryRowContext(ctx, `
SELECT count(*) FROM task_scope WHERE task_id=$1 AND kind IN ('root_domain','subdomain','ip','cidr')`,
		taskID).Scan(&decidableKinds); err != nil {
		return false, false, err
	}
	return false, decidableKinds > 0, nil
}

// SrcFindingTraffic exposes the finding's bound evidence to SRC callers (delivery
// readiness/合规判定需要绑定了哪些流量、以及每条流量的请求头快照).
func (s *SrcStore) SrcFindingTraffic(ctx context.Context, findingID int64) (*FindingTraffic, error) {
	if s == nil || s.db == nil || findingID <= 0 {
		return nil, nil
	}
	return s.db.GetFindingTraffic(ctx, findingID)
}

// SrcFinding loads one finding row (nil when missing). 台账准备需要它的类型/等级/正文，
// 与导出面读的是同一行，避免"导出看到的"和"台账记的"不一致。
func (s *SrcStore) SrcFinding(ctx context.Context, findingID int64) (*DBFinding, error) {
	if s == nil || s.db == nil || findingID <= 0 {
		return nil, nil
	}
	return s.db.GetFinding(findingID)
}
