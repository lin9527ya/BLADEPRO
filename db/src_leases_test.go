package db

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSrcCellLeases(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skipf("no postgres configured (%v) — skipping", err)
	}
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	task, err := d.CreateTaskWithOptions("src-lease-test", "租约", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	ctx := context.Background()
	store := d.Src()
	suffix := time.Now().UnixNano()
	a1, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: fmt.Sprintf("lease-a-%d.example", suffix), TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	a2, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: fmt.Sprintf("lease-b-%d.example", suffix), TaskID: task.ID})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM src_cell_leases WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM assets WHERE id = ANY($1)`, []int64{a1, a2})
	})

	acq, busy, err := store.AcquireSrcLeases(ctx, task.ID, []int64{a1}, "sqli", 0, "planner", SrcLeaseTTL)
	if err != nil || len(busy) != 0 || len(acq) != 1 {
		t.Fatalf("first acquire: acq=%v busy=%v err=%v", acq, busy, err)
	}
	acq, busy, err = store.AcquireSrcLeases(ctx, task.ID, []int64{a1}, "sqli", 99, "worker-1", SrcLeaseTTL)
	if err != nil || len(acq) != 0 || len(busy) != 1 || busy[0].AssetID != a1 {
		t.Fatalf("second acquire should be busy: acq=%v busy=%v err=%v", acq, busy, err)
	}

	// 全有或全无：a1 被占时，a2 也不该留下租约。
	acq, busy, err = store.AcquireSrcLeases(ctx, task.ID, []int64{a1, a2}, "sqli", 0, "planner", SrcLeaseTTL)
	if err != nil || len(acq) != 0 || len(busy) != 1 {
		t.Fatalf("multi acquire: acq=%v busy=%v err=%v", acq, busy, err)
	}
	active, err := store.ListActiveSrcLeases(ctx, task.ID)
	if err != nil || len(active) != 1 || active[0].AssetID != a1 {
		t.Fatalf("a2 不该留下租约：%v err=%v", active, err)
	}

	if err := store.BindSrcLeaseIntent(ctx, task.ID, []int64{a1}, "sqli", 42); err != nil {
		t.Fatal(err)
	}
	active, _ = store.ListActiveSrcLeases(ctx, task.ID)
	if len(active) != 1 || active[0].OwnerIntentID != 42 {
		t.Fatalf("bind 未生效：%+v", active)
	}
	if ok, err := store.RenewSrcLeases(ctx, task.ID, 42, SrcLeaseTTL); err != nil || !ok {
		t.Fatalf("renew: ok=%v err=%v", ok, err)
	}
	if err := store.ReleaseSrcLeases(ctx, task.ID, 42); err != nil {
		t.Fatal(err)
	}
	active, _ = store.ListActiveSrcLeases(ctx, task.ID)
	if len(active) != 0 {
		t.Fatalf("release 后仍有租约：%+v", active)
	}

	// 占位租约回滚（意图创建失败）。
	if _, _, err := store.AcquireSrcLeases(ctx, task.ID, []int64{a2}, "xss", 0, "planner", SrcLeaseTTL); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseSrcLeasesByCells(ctx, task.ID, []int64{a2}, "xss"); err != nil {
		t.Fatal(err)
	}
	if active, _ := store.ListActiveSrcLeases(ctx, task.ID); len(active) != 0 {
		t.Fatalf("占位租约未回滚：%+v", active)
	}

	// 过期后可被重新占用。
	if _, _, err := store.AcquireSrcLeases(ctx, task.ID, []int64{a1}, "ssti", 0, "planner", 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	acq, busy, err = store.AcquireSrcLeases(ctx, task.ID, []int64{a1}, "ssti", 7, "worker-2", SrcLeaseTTL)
	if err != nil || len(busy) != 0 || len(acq) != 1 {
		t.Fatalf("过期租约应可被重占：acq=%v busy=%v err=%v", acq, busy, err)
	}
	if err := store.ReleaseSrcLeases(ctx, task.ID, 7); err != nil {
		t.Fatal(err)
	}
}
