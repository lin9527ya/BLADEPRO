package evidence

// 证据产物（截图/附件）：内容按 sha256 内容寻址，落在 <Dir>/artifacts/<sha>.bin，
// 元数据写 evidence_artifacts。与 traffic 证据分开：这里是"我们自己生成/采集的
// 文件"，不是目标回包快照。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lin9527ya/bladepro/db"
)

const maxArtifactBytes = 25 << 20

// PutArtifact stores one artifact and returns its metadata row. The content is
// hashed while copying; a caller-provided sha256 is verified, never trusted.
func (s *Store) PutArtifact(ctx context.Context, in db.EvidenceArtifact, r io.Reader) (*db.EvidenceArtifact, error) {
	if s == nil || s.DB == nil || r == nil {
		return nil, fmt.Errorf("evidence store unavailable")
	}
	if in.Kind == "" {
		in.Kind = "screenshot"
	}
	stage := filepath.Join(s.Dir, "artifacts", ".staging")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(stage, "artifact-")
	if err != nil {
		return nil, err
	}
	defer func() { f.Close(); os.Remove(f.Name()) }()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(r, maxArtifactBytes+1))
	if err != nil {
		return nil, err
	}
	if n > maxArtifactBytes {
		return nil, fmt.Errorf("证据产物超过 %d MB", maxArtifactBytes>>20)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if in.SHA256 != "" && in.SHA256 != sum {
		return nil, fmt.Errorf("证据产物哈希不匹配")
	}
	if in.Size > 0 && in.Size != n {
		return nil, fmt.Errorf("证据产物大小不匹配：预期 %d，实际 %d", in.Size, n)
	}
	if err := f.Sync(); err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	path := filepath.Join(s.Dir, "artifacts", sum+".bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err := os.Rename(f.Name(), path); err != nil {
			return nil, err
		}
	}
	// MIME：优先 caller 给的值；否则按前 512 字节嗅探（截图一定是 image/*）。
	if strings.TrimSpace(in.MIME) == "" {
		head := make([]byte, 512)
		if hf, err := os.Open(path); err == nil {
			nhead, _ := io.ReadFull(hf, head)
			hf.Close()
			in.MIME = http.DetectContentType(head[:nhead])
		}
	}
	in.SHA256 = sum
	in.Size = n
	row, err := s.DB.CreateEvidenceArtifact(ctx, &in)
	if err != nil {
		return nil, err
	}
	return row, nil
}

// OpenArtifact opens the stored bytes and returns its metadata.
func (s *Store) OpenArtifact(ctx context.Context, id int64) (io.ReadCloser, *db.EvidenceArtifact, error) {
	if s == nil || s.DB == nil {
		return nil, nil, fmt.Errorf("evidence store unavailable")
	}
	a, err := s.DB.GetEvidenceArtifact(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if a == nil {
		return nil, nil, os.ErrNotExist
	}
	path := filepath.Join(s.Dir, "artifacts", a.SHA256+".bin")
	f, err := os.Open(path)
	if err != nil {
		return nil, a, err
	}
	return f, a, nil
}
