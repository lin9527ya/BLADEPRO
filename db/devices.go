package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// 设备注册表（移植自 quiver devices）：adb / ssh / local 三类。
// 凭据字段默认不返回给列表；DeviceByID 返回完整记录供宿主工具执行时使用。
type Device struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Kind       string     `json:"kind"`
	Conn       string     `json:"conn,omitempty"`
	SSHHost    string     `json:"ssh_host,omitempty"`
	SSHPort    int        `json:"ssh_port,omitempty"`
	SSHUser    string     `json:"ssh_user,omitempty"`
	SSHKey     string     `json:"ssh_key,omitempty"`
	ToolsMD    string     `json:"tools_md,omitempty"`
	SkillName  string     `json:"skill_name,omitempty"`
	Status     string     `json:"status"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	ProbeNote  string     `json:"probe_note,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
}

const deviceCols = `id, name, kind, COALESCE(conn,''), COALESCE(ssh_host,''), ssh_port,
	COALESCE(ssh_user,''), COALESCE(ssh_key,''), COALESCE(tools_md,''), COALESCE(skill_name,''),
	status, last_seen_at, COALESCE(probe_note,''), created_at`

func scanDevice(sc interface{ Scan(dest ...any) error }) (*Device, error) {
	var d Device
	var seen sql.NullTime
	if err := sc.Scan(&d.ID, &d.Name, &d.Kind, &d.Conn, &d.SSHHost, &d.SSHPort,
		&d.SSHUser, &d.SSHKey, &d.ToolsMD, &d.SkillName, &d.Status, &seen, &d.ProbeNote, &d.CreatedAt); err != nil {
		return nil, err
	}
	if seen.Valid {
		d.LastSeenAt = &seen.Time
	}
	return &d, nil
}

// ListDevices 列出设备（不含密码；ssh_key 只给路径）。
func (s *AssetStore) ListDevices() ([]Device, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	rows, err := s.db.Query(`SELECT ` + deviceCols + ` FROM devices ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeviceByID 取单个设备（含 ssh 密码，仅供宿主执行）。
func (s *AssetStore) DeviceByID(id int64) (*Device, string, error) {
	if s == nil || s.db == nil {
		return nil, "", errDBClosed
	}
	var d Device
	var seen sql.NullTime
	var pass string
	row := s.db.QueryRow(`SELECT `+deviceCols+`, COALESCE(ssh_pass,'') FROM devices WHERE id=$1`, id)
	if err := row.Scan(&d.ID, &d.Name, &d.Kind, &d.Conn, &d.SSHHost, &d.SSHPort,
		&d.SSHUser, &d.SSHKey, &d.ToolsMD, &d.SkillName, &d.Status, &seen, &d.ProbeNote, &d.CreatedAt, &pass); err != nil {
		if err == sql.ErrNoRows {
			return nil, "", nil
		}
		return nil, "", err
	}
	if seen.Valid {
		d.LastSeenAt = &seen.Time
	}
	return &d, pass, nil
}

// UpsertDevice 新建或更新设备（id=0 新建）。
func (s *AssetStore) UpsertDevice(d Device, sshPass string) (*Device, error) {
	if s == nil || s.db == nil {
		return nil, errDBClosed
	}
	d.Kind = strings.ToLower(strings.TrimSpace(d.Kind))
	if d.Kind != "adb" && d.Kind != "ssh" && d.Kind != "local" {
		return nil, errBadInput("kind 只能是 adb/ssh/local")
	}
	if strings.TrimSpace(d.Name) == "" {
		return nil, errBadInput("name 必填")
	}
	if d.SSHPort <= 0 {
		d.SSHPort = 22
	}
	var row *sql.Row
	if d.ID > 0 {
		row = s.db.QueryRow(`UPDATE devices SET name=$2, kind=$3, conn=$4, ssh_host=$5, ssh_port=$6,
			ssh_user=$7, ssh_key=$8, tools_md=$9, skill_name=$10,
			ssh_pass=CASE WHEN $11='' THEN ssh_pass ELSE $11 END
			WHERE id=$1 RETURNING `+deviceCols,
			d.ID, d.Name, d.Kind, d.Conn, d.SSHHost, d.SSHPort, d.SSHUser, d.SSHKey, d.ToolsMD, d.SkillName, sshPass)
	} else {
		row = s.db.QueryRow(`INSERT INTO devices(name,kind,conn,ssh_host,ssh_port,ssh_user,ssh_key,ssh_pass,tools_md,skill_name)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+deviceCols,
			d.Name, d.Kind, d.Conn, d.SSHHost, d.SSHPort, d.SSHUser, d.SSHKey, sshPass, d.ToolsMD, d.SkillName)
	}
	out, err := scanDevice(row)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("设备不存在")
	}
	return out, err
}

// DeleteDevice 删除设备。
func (s *AssetStore) DeleteDevice(id int64) error {
	if s == nil || s.db == nil {
		return errDBClosed
	}
	_, err := s.db.Exec(`DELETE FROM devices WHERE id=$1`, id)
	return err
}

// RecordDeviceProbe 记录一次探活结果。
func (s *AssetStore) RecordDeviceProbe(id int64, online bool, note string) error {
	if s == nil || s.db == nil {
		return errDBClosed
	}
	status := "offline"
	if online {
		status = "online"
	}
	_, err := s.db.Exec(`UPDATE devices SET status=$2, probe_note=$3,
		last_seen_at=CASE WHEN $2='online' THEN now() ELSE last_seen_at END WHERE id=$1`, id, status, strings.TrimSpace(note))
	return err
}
