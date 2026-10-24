package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

// TestAddIntentRespectsCellLease 钉住每格租约：同一 (资产 × 风险面) 只能有一条在测的
// 意图；换风险面仍可并行派。租约在 add_intent 时占，避免 planner 换措辞重派。
func TestAddIntentRespectsCellLease(t *testing.T) {
	d := testDB(t)
	defer d.Close()
	task, err := d.CreateTaskWithOptions("src-lease-intent", "格租约", db.TaskCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })
	assetID, err := d.Assets().UpsertSubdomain(db.UpsertSubdomainReq{
		Domain: fmt.Sprintf("lease-intent-%d.example", time.Now().UnixNano()), TaskID: task.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	tools := NewToolSet(d.Exploration(task.ExplorationID), "planner")
	tools.SetTaskID(task.ID)
	tools.SetAssetStore(d.Assets(), d.Companies())
	tools.SetSrcMode(srcmode.DefaultDoc(), d.Src())

	raw, _ := json.Marshal(assetID)
	if _, _, err := tools.addOneIntent(intentItem{
		Summary: "SQL 注入探测", AssetIDs: []json.RawMessage{raw}, RiskArea: "sqli",
	}, toolsQ()); err != nil {
		t.Fatalf("第一条 sqli 意图应放行：%v", err)
	}
	_, _, err = tools.addOneIntent(intentItem{
		Summary: "换个说法的 SQL 注入", AssetIDs: []json.RawMessage{raw}, RiskArea: "sqli",
	}, toolsQ())
	if err == nil || !strings.Contains(err.Error(), "别重复派") {
		t.Fatalf("同格第二条意图必须被租约拒绝，got %v", err)
	}
	if _, _, err := tools.addOneIntent(intentItem{
		Summary: "同资产的 XSS 探测", AssetIDs: []json.RawMessage{raw}, RiskArea: "xss",
	}, toolsQ()); err != nil {
		t.Fatalf("不同风险面应可并行：%v", err)
	}
}
