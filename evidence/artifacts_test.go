package evidence

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lin9527ya/bladepro/db"
)

func TestPutAndOpenArtifact(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
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
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	payload := encoded.Bytes()
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

func TestArtifactReadRejectsCorruptionAndFakeImage(t *testing.T) {
	dsn, _, err := db.DSN()
	if err != nil {
		t.Skip("isolated DB required")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	task, err := pg.CreateTask("artifact integrity", "fixture", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.DeleteTask(task.ID)
	store := New(pg, nil, t.TempDir())
	if _, err := store.PutArtifact(t.Context(), db.EvidenceArtifact{TaskID: task.ID, Kind: "screenshot", MIME: "image/png"}, strings.NewReader("not an image")); err == nil {
		t.Fatal("fake MIME accepted as screenshot")
	}
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	artifact, err := store.PutArtifact(t.Context(), db.EvidenceArtifact{TaskID: task.ID, Kind: "screenshot"}, bytes.NewReader(imageData.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir, "artifacts", artifact.SHA256+".bin")
	if err := os.WriteFile(path, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.OpenArtifact(t.Context(), artifact.ID); err == nil {
		t.Fatal("corrupt artifact was downloadable")
	}
}
