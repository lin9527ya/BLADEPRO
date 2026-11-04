package db

import (
	"fmt"
	"testing"
	"time"
)

// 穷尽前置门的数据来源：范围内还有资产一条意图都没派过、或只终结了很少几类风险。
func TestUntouchedScopeAssetsAndDepthGaps(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	task, err := d.CreateTaskWithOptions("coverage-gaps", "穷尽门", TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	root := fmt.Sprintf("gaps-%d.example", time.Now().UnixNano())
	wwwDomain := "www." + root
	mailDomain := "mail." + root
	wwwID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: wwwDomain, TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert www: %v", err)
	}
	mailID, err := d.Assets().UpsertSubdomain(UpsertSubdomainReq{Domain: mailDomain, TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert mail: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM assets WHERE domain = ANY($1::text[])`, []string{wwwDomain, mailDomain})
	})
	if _, err := d.Assets().AddAgentScope(task.ID, "root_domain", root, "测试范围", "agent"); err != nil {
		t.Fatalf("add scope: %v", err)
	}

	ts := d.Exploration(task.ExplorationID)
	// www 派过一条已终结的意图（sqli），mail 一条都没派过。
	if _, err := ts.AddNode(KindIntent, map[string]any{
		"summary":   "对 www 做注入验证",
		"risk_area": "sqli",
	}, 5, "done", "planner", []int64{wwwID}); err != nil {
		t.Fatalf("add done intent: %v", err)
	}

	pending, total, err := d.Assets().UntouchedScopeAssets(task.ID, 8)
	if err != nil {
		t.Fatalf("UntouchedScopeAssets: %v", err)
	}
	if total != 1 || len(pending) != 1 || pending[0].ID != mailID {
		t.Fatalf("只应剩 mail 未触达（total=%d）：%+v", total, pending)
	}

	gaps, gtotal, err := ts.DepthGaps(task.ID, DefaultDepthAreas, 8)
	if err != nil {
		t.Fatalf("DepthGaps: %v", err)
	}
	if gtotal != 2 {
		t.Fatalf("两个资产都未达到深度阈值，应返回 2，实际 %d：%+v", gtotal, gaps)
	}
	var found bool
	for _, g := range gaps {
		if g.AssetID != wwwID {
			continue
		}
		found = true
		if len(g.DoneAreas) != 1 || g.DoneAreas[0] != "sqli" {
			t.Fatalf("www 的已终结风险类应为 [sqli]，实际 %v", g.DoneAreas)
		}
	}
	if !found {
		t.Fatalf("深度缺口应包含已终结 1 类的 www：%+v", gaps)
	}
}
