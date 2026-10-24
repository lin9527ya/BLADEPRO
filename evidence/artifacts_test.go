package evidence

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestPutAndOpenArtifact(t *testing.T) {
	dsn := os.Getenv("BLADEPRO_PG_DSN")
	if dsn == "" {
		t.Skip("BLADEPRO_PG_DSN is required for evidence integration tests")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	task, err := pg.CreateTask("artifact "+t.Name(), "local fixtures", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.DeleteTask(task.ID) })
	store := New(pg, nil, t.TempDir())
	// 最小 PNG 头足够让 mime 嗅探命中 image/png。
	payload := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 128)...)
	ctx := context.Background()
	a, err := store.PutArtifact(ctx, db.EvidenceArtifact{
		TaskID: task.ID, Kind: "screenshot", Filename: "shot.png",
	}, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	if a.MIME != "image/png" || a.Size != int64(len(payload)) || len(a.SHA256) != 64 {
		t.Fatalf("artifact=%+v", a)
	}
	rc, got, err := store.OpenArtifact(ctx, a.ID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if !bytes.Equal(data, payload) || got.SHA256 != a.SHA256 {
		t.Fatalf("content mismatch (got %d bytes)", len(data))
	}
	if _, err := store.PutArtifact(ctx, db.EvidenceArtifact{
		TaskID: task.ID, Kind: "screenshot", SHA256: "deadbeef",
	}, bytes.NewReader(payload)); err == nil {
		t.Fatal("wrong sha256 should be rejected")
	}
}
