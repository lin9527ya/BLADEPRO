package srcmode

import (
	"context"

	"github.com/lin9527ya/bladepro/db"
)

// checkReconFinish 是「只做信息收集」（recon_only）任务的收尾门：唯一的完成条件是
// 任务范围内已经登记了资产（insert_assets 落库）。它刻意不要求覆盖格与收尾项——
// 侦察任务不做漏洞测试；但也不允许"一条资产都没有"就收尾（fail-closed）。
func checkReconFinish(ctx context.Context, store *db.SrcStore, taskID int64, doc ModeDoc) (Report, error) {
	rep := Report{TaskID: taskID, GapsByArea: map[string]int{}, Gaps: []Gap{}}
	if store == nil || taskID <= 0 {
		// 与 CheckFinish 同口径：没有存储（单测/未接线）时不臆造缺口。
		rep.Finishable = true
		return rep, nil
	}
	_ = doc
	assets, err := store.InScopeSrcAssets(ctx, taskID)
	if err != nil {
		return rep, err
	}
	rep.Assets = len(assets)
	if len(assets) == 0 {
		rep.Gaps = append(rep.Gaps, Gap{
			Asset: "任务范围", AssetType: "scope", Area: "recon", AreaTitle: "信息收集", Item: "assets",
			Kind:     GapNoAssets,
			Reason:   "侦察任务还没有登记任何资产：先按 src-recon 收集企业面/域名，再用 insert_assets 落库",
			Priority: 100,
		})
		rep.GapsByArea["recon"] = 1
		rep.GapsTotal = 1
		rep.BlockedReason = "信息收集尚未产出任何资产"
		return rep, nil
	}
	rep.Finishable = true
	return rep, nil
}
