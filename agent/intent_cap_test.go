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

// TestAddIntentRespectsPerAssetCap 钉住 mode_doc.max_intents_per_asset 的执行点：
// 它过去只写在提示词里，planner 可以无上限地换措辞重派同一资产。
func TestAddIntentRespectsPerAssetCap(t *testing.T) {
	d := testDB(t)
	defer d.Close()

	task, err := d.CreateTaskWithOptions("src-intent-cap", "每资产意图上限", db.TaskCreateOptions{})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	t.Cleanup(func() { _ = d.DeleteTask(task.ID) })

	domain := fmt.Sprintf("cap-%d.example", time.Now().UnixNano())
	assetID, err := d.Assets().UpsertSubdomain(db.UpsertSubdomainReq{Domain: domain, TaskID: task.ID})
	if err != nil {
		t.Fatalf("upsert subdomain: %v", err)
	}

	tools := NewToolSet(d.Exploration(task.ExplorationID), "planner")
	tools.SetTaskID(task.ID)
	tools.SetAssetStore(d.Assets(), d.Companies())
	doc := srcmode.DefaultDoc()
	doc.MaxIntentsPerAsset = 1
	tools.SetSrcMode(doc, d.Src())

	raw, _ := json.Marshal(assetID)
	if _, _, err := tools.addOneIntent(intentItem{
		Summary: "第一条：测该资产的越权", AssetIDs: []json.RawMessage{raw},
	}, toolsQ()); err != nil {
		t.Fatalf("第一条意图应放行：%v", err)
	}
	_, _, err = tools.addOneIntent(intentItem{
		Summary: "第二条：换个说法再测同一资产", AssetIDs: []json.RawMessage{raw},
	}, toolsQ())
	if err == nil || !strings.Contains(err.Error(), "max_intents_per_asset") {
		t.Fatalf("超出每资产上限必须被拒，got %v", err)
	}
}
