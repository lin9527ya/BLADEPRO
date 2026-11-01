package server

// 单条发现的交付包：一条发现 = md/docx + PoC + 属于它的截图。
//
// 批量包的截图曾经按任务全量复制，导致每条发现都带同一批图；这里把"截图归属"
// 收敛为 finding 的 asset_ids / node_id，单条包与批量包共用同一个过滤器。

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"strings"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/report"
	"github.com/lin9527ya/bladepro/srcmode"
)

type srcZipAdd func(name string, data []byte) error

func (s *Server) srcFindingScreenshots(ctx context.Context, f *db.DBFinding) []db.EvidenceArtifact {
	if f == nil || f.TaskID == nil || *f.TaskID <= 0 {
		return nil
	}
	arts, err := s.m.pg.ListEvidenceArtifacts(ctx, *f.TaskID)
	if err != nil {
		return nil
	}
	return filterSrcFindingScreenshots(f, arts)
}

// filterSrcFindingScreenshots keeps images attached to the finding's assets or
// its evidence node. Task-wide screenshots are not evidence for every finding.
func filterSrcFindingScreenshots(f *db.DBFinding, arts []db.EvidenceArtifact) []db.EvidenceArtifact {
	if f == nil {
		return nil
	}
	assets := map[int64]bool{}
	for _, id := range f.AssetIDs {
		if id > 0 {
			assets[id] = true
		}
	}
	nodeID := int64(0)
	if f.NodeID != nil {
		nodeID = *f.NodeID
	}
	out := make([]db.EvidenceArtifact, 0)
	for _, a := range arts {
		if !strings.HasPrefix(a.MIME, "image/") {
			continue
		}
		if (a.AssetID > 0 && assets[a.AssetID]) || (nodeID > 0 && a.NodeID == nodeID) {
			out = append(out, a)
		}
	}
	return out
}

func srcScreenshotPath(a db.EvidenceArtifact) string {
	ext := strings.ToLower(filepath.Ext(a.Filename))
	if ext == "" {
		switch a.MIME {
		case "image/jpeg":
			ext = ".jpg"
		default:
			ext = ".png"
		}
	}
	base := strings.TrimSuffix(a.Filename, filepath.Ext(a.Filename))
	return fmt.Sprintf("attachments/screenshots/%d-%s%s", a.ID, srcFileSlug(base, "screenshot"), ext)
}

// addSrcFindingBundle writes the finding's md/docx/PoC and its own screenshots.
// seen lets the task-level zip dedupe screenshots shared by two findings.
func (s *Server) addSrcFindingBundle(ctx context.Context, add srcZipAdd, f *db.DBFinding, doc srcmode.ModeDoc, base string, seen map[int64]bool) error {
	in, warnings := s.buildSrcInput(ctx, f)
	if len(warnings) > 0 {
		log.Printf("[src] 交付包：发现 #%d 告警 %d 条", f.ID, len(warnings))
	}
	if doc.StrictLayout {
		in.Notes = warningNotes(in.Notes)
	}
	if issues := appendExportIssues(&in); len(issues) > 0 {
		log.Printf("[src] 交付包：发现 #%d 正文校验 %d 条", f.ID, len(issues))
	}
	if err := add(base+".md", []byte(crlfText(report.SrcMarkdown(in)))); err != nil {
		return err
	}
	docx, err := report.SrcDOCX(in)
	if err != nil {
		return err
	}
	if err := add(base+".docx", docx); err != nil {
		return err
	}
	if poc, err := s.buildSrcPoc(ctx, f); err == nil && poc != nil {
		for name, data := range poc.Bodies {
			if err := add(poc.Dir+"/"+name, data); err != nil {
				return err
			}
		}
		for name, data := range map[string]string{
			"poc.sh": poc.Script, "request.http": poc.HTTP, "README.txt": poc.Readme,
		} {
			if err := add(poc.Dir+"/"+name, []byte(data)); err != nil {
				return err
			}
		}
	}
	for _, a := range s.srcFindingScreenshots(ctx, f) {
		if seen != nil && seen[a.ID] {
			continue
		}
		if seen != nil {
			seen[a.ID] = true
		}
		rc, _, err := s.evidenceStore().OpenArtifact(ctx, a.ID)
		if err != nil {
			log.Printf("[src] 交付包：读取证据产物 #%d 失败: %v", a.ID, err)
			continue
		}
		data, err := io.ReadAll(io.LimitReader(rc, 25<<20))
		rc.Close()
		if err != nil {
			log.Printf("[src] 交付包：读取证据产物 #%d 失败: %v", a.ID, err)
			continue
		}
		if err := add(srcScreenshotPath(a), data); err != nil {
			return err
		}
	}
	return nil
}

func srcFindingReadme(f *db.DBFinding) string {
	var b strings.Builder
	b.WriteString("# SRC 单条交付包\n\n")
	fmt.Fprintf(&b, "- 发现：#%d\n- 标题：%s\n- 等级：%s\n", f.ID, report.FindingTitle(f), report.SrcSeverityLabel(f.Severity))
	if f.TaskID != nil && *f.TaskID > 0 {
		fmt.Fprintf(&b, "- 任务：#%d\n", *f.TaskID)
	}
	b.WriteString("\n正文见 findings/ 目录；PoC 与截图见 attachments/ 目录。\n")
	b.WriteString("\n提交前请按平台页面核对必填字段；正文中的 Cookie/Authorization/token 等敏感值已脱敏，原始字节保留在内部证据库。\n")
	return b.String()
}

func (s *Server) buildSrcFindingZip(ctx context.Context, f *db.DBFinding) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		w, err := zw.Create(name)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := add("提交说明.md", []byte(srcFindingReadme(f))); err != nil {
		return nil, err
	}
	if sub, err := s.m.PG().Src().GetSrcSubmissionByFinding(ctx, f.ID, ""); err == nil && sub != nil {
		if blob, err := json.MarshalIndent(sub, "", "  "); err == nil {
			if err := add("submission.json", blob); err != nil {
				return nil, err
			}
		}
	}
	doc := srcmode.ModeDoc{}
	if f.TaskID != nil && *f.TaskID > 0 {
		doc = s.srcDocForTask(*f.TaskID)
	}
	base := fmt.Sprintf("findings/%d-%s", f.ID, srcFileSlug(report.FindingTitle(f), f.VulnClass))
	if err := s.addSrcFindingBundle(ctx, add, f, doc, base, nil); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
