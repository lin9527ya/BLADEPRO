package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"testing"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

// A screenshot of asset A must not satisfy asset B's screenshots closure item.
func TestScreenshotArtifactMustMatchAsset(t *testing.T) {
	const host = "shot-binding.example"
	m, task, asset := p0Fixture(t, host)
	s := &Server{m: m}
	png, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	art, err := s.evidenceStore().PutArtifact(context.Background(), db.EvidenceArtifact{
		TaskID: parseIntDefault(task.ID), AssetID: asset, Kind: "screenshot",
	}, bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	node, err := task.Store.AddNode(db.KindFact, map[string]any{"summary": "截图证据", "asset_ids": []int64{asset}}, 1, "confirmed", "test", []int64{asset})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.pg.SetEvidenceArtifactNode(context.Background(), art.ID, node); err != nil {
		t.Fatal(err)
	}
	var other int64
	if err := m.pg.QueryRow(`INSERT INTO assets(type,url,domain) VALUES('service',$1,$2) RETURNING id`, "http://"+host+":8443", host).Scan(&other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = m.pg.Exec(`DELETE FROM assets WHERE id=$1`, other) })
	doc := srcmode.DefaultDoc()
	chk := db.SrcCheck{TaskID: parseIntDefault(task.ID), AssetID: asset, CheckKey: "screenshots", State: db.SrcCheckDone, EvidenceNodeID: node}
	if err := srcmode.ValidateCheck(context.Background(), m.pg.Src(), chk.TaskID, doc, chk); err != nil {
		t.Fatalf("same-asset screenshot rejected: %v", err)
	}
	chk.AssetID = other
	if err := srcmode.ValidateCheck(context.Background(), m.pg.Src(), chk.TaskID, doc, chk); err == nil {
		t.Fatal("cross-asset screenshot accepted")
	}
}
