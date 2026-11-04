package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 测试账号池（移植自 quiver test_accounts；维度用 company_id，兼容历史 vendor_id）。
//
// 凭据默认不返回：List 只给掩码，GetCredential 才返回明文——由 server 的
// get_account_credential 工具与探活逻辑使用，调用方负责脱敏与审计。
type TestAccount struct {
	ID             int64      `json:"id"`
	CompanyID      int64      `json:"company_id"`
	Label          string     `json:"label"`
	RoleTier       string     `json:"role_tier"`
	CredType       string     `json:"cred_type"`
	CredMasked     string     `json:"cred_masked,omitempty"`
	OwnerPhone     string     `json:"owner_phone,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Status         string     `json:"status"`
	LastCheckedAt  *time.Time `json:"last_checked_at,omitempty"`
	CheckNote      string     `json:"check_note,omitempty"`
	CheckFailStreak int       `json:"check_fail_streak"`
	CreatedAt      time.Time  `json:"created_at"`
}

var (
	AccountRoleTiers = []string{"admin", "normal", "readonly", "vip"}
	AccountCredTypes = []string{"cookie", "token", "password"}
	AccountStatuses  = []string{"active", "expired", "banned"}
)

func normalizeAccountEnum(v string, allowed []string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	for _, a := range allowed {
		if v == a {
			return v
		}
	}
	return ""
}

// maskCredential 只保留末 4 位（短于 8 位时全掩码）。
func maskCredential(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) < 8 {
		return "****"
	}
	return "****" + string(r[len(r)-4:])
}

const accountCols = `id, COALESCE(company_id,0), label, role_tier, cred_type, cred_blob,
	owner_phone, expires_at, status, last_checked_at, check_note, check_fail_streak, created_at`

func scanAccount(sc interface {
	Scan(dest ...any) error
}, masked bool) (*TestAccount, error) {
	var a TestAccount
	var cred string
	var expires, checked sql.NullTime
	if err := sc.Scan(&a.ID, &a.CompanyID, &a.Label, &a.RoleTier, &a.CredType, &cred,
		&a.OwnerPhone, &expires, &a.Status, &checked, &a.CheckNote, &a.CheckFailStreak, &a.CreatedAt); err != nil {
		return nil, err
	}
	if masked {
		a.CredMasked = maskCredential(cred)
	}
	if expires.Valid {
		a.ExpiresAt = &expires.Time
	}
	if checked.Valid {
		a.LastCheckedAt = &checked.Time
	}
	return &a, nil
}

// CreateTestAccount 新建账号（凭据明文入库；列表接口只返回掩码）。
func (s *AssetStore) CreateTestAccount(a TestAccount, credential string) (*TestAccount, error) {
	if s == nil || s.db == nil {
		return nil, errDBClosed
	}
	if a.CompanyID <= 0 {
		return nil, errBadInput("company_id 必填")
	}
	if strings.TrimSpace(a.Label) == "" {
		return nil, errBadInput("label 必填")
	}
	if normalizeAccountEnum(a.RoleTier, AccountRoleTiers) == "" {
		return nil, errBadInput("role_tier 只能是 admin/normal/readonly/vip")
	}
	if normalizeAccountEnum(a.CredType, AccountCredTypes) == "" {
		return nil, errBadInput("cred_type 只能是 cookie/token/password")
	}
	if strings.TrimSpace(credential) == "" {
		return nil, errBadInput("凭据不能为空")
	}
	row := s.db.QueryRow(`INSERT INTO test_accounts(company_id, label, role_tier, cred_type, cred_blob, owner_phone, expires_at, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'active') RETURNING `+accountCols,
		a.CompanyID, strings.TrimSpace(a.Label), a.RoleTier, a.CredType, credential,
		strings.TrimSpace(a.OwnerPhone), a.ExpiresAt)
	return scanAccount(row, true)
}

// ListTestAccounts 列出账号（companyID=0 表示全部），凭据只给掩码。
func (s *AssetStore) ListTestAccounts(companyID int64, limit int) ([]TestAccount, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT `+accountCols+` FROM test_accounts
		WHERE COALESCE(NULLIF($1,0), company_id) = company_id
		ORDER BY company_id, role_tier, label LIMIT $2`, companyID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TestAccount{}
	for rows.Next() {
		a, err := scanAccount(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// UpdateTestAccount 更新元数据（不允许改凭据；换凭据用 SetTestAccountCredential）。
func (s *AssetStore) UpdateTestAccount(id int64, label, roleTier, status, ownerPhone string, expiresAt *time.Time) (*TestAccount, error) {
	if s == nil || s.db == nil {
		return nil, errDBClosed
	}
	role, st := roleTier, status
	if role != "" {
		role = normalizeAccountEnum(role, AccountRoleTiers)
		if role == "" {
			return nil, errBadInput("role_tier 不合法")
		}
	}
	if st != "" {
		st = normalizeAccountEnum(st, AccountStatuses)
		if st == "" {
			return nil, errBadInput("status 不合法")
		}
	}
	row := s.db.QueryRow(`UPDATE test_accounts SET
		label=COALESCE(NULLIF($2,''), label),
		role_tier=COALESCE(NULLIF($3,''), role_tier),
		status=COALESCE(NULLIF($4,''), status),
		owner_phone=COALESCE(NULLIF($5,''), owner_phone),
		expires_at=COALESCE($6, expires_at)
		WHERE id=$1 RETURNING `+accountCols,
		id, strings.TrimSpace(label), role, st, strings.TrimSpace(ownerPhone), expiresAt)
	a, err := scanAccount(row, true)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// SetTestAccountCredential 轮换凭据（写审计时不要把明文写进日志）。
func (s *AssetStore) SetTestAccountCredential(id int64, credential string) error {
	if s == nil || s.db == nil {
		return errDBClosed
	}
	if strings.TrimSpace(credential) == "" {
		return errBadInput("凭据不能为空")
	}
	_, err := s.db.Exec(`UPDATE test_accounts SET cred_blob=$2, status='active', check_fail_streak=0 WHERE id=$1`, id, credential)
	return err
}

// GetTestAccountCredential 返回明文凭据（仅供探活与宿主工具；调用方负责审计脱敏）。
func (s *AssetStore) GetTestAccountCredential(id int64) (*TestAccount, string, error) {
	if s == nil || s.db == nil {
		return nil, "", errDBClosed
	}
	row := s.db.QueryRow(`SELECT `+accountCols+` FROM test_accounts WHERE id=$1`, id)
	var a TestAccount
	var cred string
	var expires, checked sql.NullTime
	if err := row.Scan(&a.ID, &a.CompanyID, &a.Label, &a.RoleTier, &a.CredType, &cred,
		&a.OwnerPhone, &expires, &a.Status, &checked, &a.CheckNote, &a.CheckFailStreak, &a.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}
	a.CredMasked = maskCredential(cred)
	if expires.Valid {
		a.ExpiresAt = &expires.Time
	}
	if checked.Valid {
		a.LastCheckedAt = &checked.Time
	}
	return &a, cred, nil
}

// DeleteTestAccount 删除账号。
func (s *AssetStore) DeleteTestAccount(id int64) error {
	if s == nil || s.db == nil {
		return errDBClosed
	}
	_, err := s.db.Exec(`DELETE FROM test_accounts WHERE id=$1`, id)
	return err
}

// RecordAccountCheck 记录一次探活结果：连续失败 3 次置 banned；成功置 active 并清零。
func (s *AssetStore) RecordAccountCheck(id int64, ok bool, note string) error {
	if s == nil || s.db == nil {
		return errDBClosed
	}
	status := "active"
	streak := 0
	if !ok {
		status = "expired"
		streak = 1
	}
	_, err := s.db.Exec(`UPDATE test_accounts SET
		status = CASE WHEN $2 THEN 'active' ELSE CASE WHEN check_fail_streak + 1 >= 3 THEN 'banned' ELSE 'expired' END END,
		check_fail_streak = CASE WHEN $2 THEN 0 ELSE check_fail_streak + 1 END,
		last_checked_at = now(),
		check_note = $3
		WHERE id=$1`, id, ok, strings.TrimSpace(note))
	_ = status
	_ = streak
	return err
}

// AccountCheckThrottled 判断是否在 10 分钟节流窗口内（true = 直接返回上次结果）。
func (s *AssetStore) AccountCheckThrottled(id int64) bool {
	if s == nil || s.db == nil {
		return false
	}
	var last sql.NullTime
	if err := s.db.QueryRow(`SELECT last_checked_at FROM test_accounts WHERE id=$1`, id).Scan(&last); err != nil {
		return false
	}
	return last.Valid && time.Since(last.Time) < 10*time.Minute
}

// accountCompanyID 解析账号所属公司（探活 URL 取公司档案 check_url）。
func (s *AssetStore) AccountCompanyID(id int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, errDBClosed
	}
	var cid int64
	err := s.db.QueryRow(`SELECT COALESCE(company_id,0) FROM test_accounts WHERE id=$1`, id).Scan(&cid)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("账号 %d 不存在", id)
	}
	return cid, err
}
