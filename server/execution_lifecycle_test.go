package server

import (
	"context"
	"github.com/lin9527ya/bladepro/db"
	"sync"
	"testing"
	"time"
)

func TestExecutionAdmissionIsAtomic(t *testing.T) {
	svc, pg, _, task := newExecFixture(t)
	old, had, err := pg.GetSetting(settingExecMaxActive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = pg.SetSetting(settingExecMaxActive, old)
		} else {
			_, _ = pg.Exec(`DELETE FROM settings WHERE key=$1`, settingExecMaxActive)
		}
	})
	if err := pg.SetSetting(settingExecMaxActive, "1"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Submit(context.Background(), task, 0, "test", "sleep 30", "quota", 0)
			results <- err == nil
		}()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for ok := range results {
		if ok {
			accepted++
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d want 1", accepted)
	}
	svc.Shutdown(time.Second)
	rows, err := pg.ListExecutionsByTask(task, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if db.ExecutionActive(row.Status) {
			t.Fatal("shutdown left active execution")
		}
	}
	if _, err := svc.Submit(context.Background(), task, 0, "test", "true", "after shutdown", 0); err == nil {
		t.Fatal("shutdown accepted work")
	}
}

func TestNormalExitReapsBackgroundPipeHolder(t *testing.T) {
	svc, _, _, task := newExecFixture(t)
	st, err := svc.Submit(context.Background(), task, 0, "test", "sleep 30 & printf 'started\\n'", "background", 0)
	if err != nil {
		t.Fatal(err)
	}
	got := waitTerminal(t, svc, st.ID, 8*time.Second)
	if got.Status != db.ExecCompleted {
		t.Fatalf("background stdout kept job active: %+v", got)
	}
}
