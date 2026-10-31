package db

import (
	"context"
	"testing"
)

func TestEvidenceChangeInvalidatesPreparedSRCSubmission(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skip("isolated DB not configured")
	}
	d, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	task, err := d.CreateTask("version", "version", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer d.DeleteTask(task.ID)
	fid, err := d.AddFinding(task.ID, 0, "idor", "version", "medium", "summary", "proof", "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	version := int64(0)
	sub, meta, err := d.Src().PrepareSrcSubmission(context.Background(), SrcSubmissionInput{TaskID: task.ID, FindingID: fid, Status: SrcSubReady, ReportReady: true, ExpectedEvidenceVersion: &version, Compliance: []byte(`{"ok":true}`)})
	if err != nil || !meta.ReportReady {
		t.Fatalf("prepare: %+v %v", meta, err)
	}
	tx, err := d.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := bumpEvidenceVersionTx(tx, fid); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	meta, err = d.Src().GetSrcFindingMeta(context.Background(), fid)
	if err != nil || meta.ReportReady {
		t.Fatalf("old proof remains ready: %+v %v", meta, err)
	}
	status, external := SrcSubSubmitted, "SRC-test"
	if _, err := d.Src().UpdateSrcSubmission(context.Background(), sub.ID, SrcSubmissionPatch{Status: &status, ExternalID: &external}); err == nil {
		t.Fatal("stale prepared submission accepted")
	}
	if _, _, err := d.Src().PrepareSrcSubmission(context.Background(), SrcSubmissionInput{TaskID: task.ID, FindingID: fid, ReportReady: true, ExpectedEvidenceVersion: &version}); err == nil {
		t.Fatal("stale rendering committed")
	}
}
