package server

// SRC 收尾门在引擎侧的落点（见 docs/SRC挖掘agent改造方案.md §6.5）。
//
// 这里只有一件事：planner 说"目标达成"不等于任务完成——覆盖矩阵的空格清零才
// 算。所以 done 之前先过一道硬门；门没过就拒绝落 done、把缺口写进时间线、并把
// 缺口摘要推给下一轮 planner。连续拒绝到上限转 policy_blocked，交人工。
//
// 为什么必须有防循环：planner 的目标函数是"(重新)证明目标已达成"，光靠提示词压
// 不住。门被拒三次还转圈，就说明这不是提示词能解决的问题了。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/lin9527ya/bladepro/agent"
	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/notify"
	"github.com/lin9527ya/bladepro/srcmode"
)

// srcGateMaxRejects 是连续拒绝上限：超过就转 policy_blocked 交人工。
const srcGateMaxRejects = 3

// srcGateState 是一个任务的收尾门拒绝状态。
type srcGateState struct {
	rejects  int
	lastHash string
}

// srcFinishGate 为 SRC 任务判定"现在能不能落 done"。
// 返回 true 表示放行（非 SRC 任务或门通过）；查询失败保留任务等待重试。
// 返回 false 表示已拒绝 done，调用方必须不落状态。
func (e *Engine) srcFinishGate(ctx context.Context, t *Task, taskID int64) bool {
	if t == nil || t.Mode != srcmode.ModeSrc {
		return true
	}
	doc := srcmode.Parse(t.ModeDoc)
	rep, err := srcmode.CheckFinish(ctx, e.m.PG().Src(), taskID, doc)
	if err != nil {
		// 基础设施错误不是完成，也不计入重复缺口拒绝次数。
		log.Printf("[src-gate] task %s 收尾门查询失败，拒绝本次完成: %v", t.ID, err)
		e.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate", IsError: true,
			Summary: "SRC 收尾门查询失败，保留未完成状态等待重试：" + err.Error()})
		return false
	}
	// 列表页读的是这次判定的快照（tasks.src_*），不是实时重算；写库失败只记录，
	// 不影响门本身。
	e.m.SetSrcProgress(t, db.SrcProgress{
		CellsTotal:  rep.CellsTotal,
		CellsDone:   rep.CellsTerminal,
		ChecksTotal: rep.ChecksTotal,
		ChecksDone:  rep.ChecksDone,
		GapsTotal:   rep.GapsTotal,
	})
	if rep.Finishable {
		e.resetSrcGate(t.ID)
		return true
	}
	e.refuseSrcFinish(t, rep)
	return false
}

// refuseSrcFinish 记录一次拒绝：时间线留痕 + 防循环计数 + （缺口变化时）推一轮提示。
func (e *Engine) refuseSrcFinish(t *Task, rep srcmode.Report) {
	hash := srcGapsHash(rep)
	summary := fmt.Sprintf("SRC 收尾门拒绝 done：%d 个缺口未清零（%d 格 / 已终态 %d，%s）",
		rep.GapsTotal, rep.CellsTotal, rep.CellsTerminal, srcGapsSummary(rep))

	e.srcMu.Lock()
	if e.srcGate == nil {
		e.srcGate = map[string]*srcGateState{}
	}
	st := e.srcGate[t.ID]
	if st == nil {
		st = &srcGateState{}
		e.srcGate[t.ID] = st
	}
	sameGaps := st.lastHash == hash
	// 只有"同一份缺口被连续拒绝"才算卡死：缺口集合一变，说明还在往前走，计数重来。
	if sameGaps {
		st.rejects++
	} else {
		st.rejects = 1
	}
	st.lastHash = hash
	rejects := st.rejects
	e.srcMu.Unlock()

	e.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate", Summary: summary})
	// 缺口集合变化时推一次 IM 告警；同一份缺口连续拒绝只留时间线，避免刷屏。
	if !sameGaps {
		body := summary
		if rep.BlockedReason != "" {
			body += "\n" + rep.BlockedReason
		}
		e.notifyTaskAlert(t, notify.EventSrcGateRejected, "SRC 收尾门拒绝 done", body, "medium")
	}

	if rejects >= srcGateMaxRejects {
		log.Printf("[src-gate] task %s 连续拒绝 %d 次，转 policy_blocked", t.ID, rejects)
		if err := e.m.SetTaskStatus(t.ID, "policy_blocked"); err != nil {
			log.Printf("[src-gate] task %s 转 policy_blocked 失败: %v", t.ID, err)
		}
		blocked := fmt.Sprintf("SRC 收尾门连续拒绝 %d 次，任务转 policy_blocked 交人工：补齐缺口格、把不适用的面标 na、或用「强制收尾」放行", rejects)
		e.emitActivity(t, db.Activity{Worker: "system", Kind: "src_gate", Summary: blocked})
		e.notifyTaskAlert(t, notify.EventSrcPolicyBlocked, "任务转 policy_blocked", blocked, "high")
		e.cancelExec(t.ID, agent.AbortSrcGateBlocked)
		return
	}
	// 同一份缺口只注入一次：反复喂同一段文字只会烧一轮 token，不会让 planner 变聪明。
	if !sameGaps {
		t.NotifySrcGate(summary)
	}
}

// resetSrcGate 在门通过（或任务复活重跑）后清掉防循环状态。
func (e *Engine) resetSrcGate(taskID string) {
	e.srcMu.Lock()
	defer e.srcMu.Unlock()
	delete(e.srcGate, taskID)
}

// srcGapsHash 是缺口集合的稳定指纹（与展示顺序无关）。
func srcGapsHash(rep srcmode.Report) string {
	rows := make([]string, 0, len(rep.Gaps))
	for _, g := range rep.Gaps {
		rows = append(rows, fmt.Sprintf("%d|%s|%s|%s", g.AssetID, g.Area, g.Item, g.Kind))
	}
	sort.Strings(rows)
	sum := sha256.Sum256([]byte(strings.Join(rows, "\n")))
	return hex.EncodeToString(sum[:8])
}

// srcGapsSummary 是给人看的缺口摘要：按面聚合前几个，够 planner 判断方向即可。
func srcGapsSummary(rep srcmode.Report) string {
	type kv struct {
		area string
		n    int
	}
	items := make([]kv, 0, len(rep.GapsByArea))
	for area, n := range rep.GapsByArea {
		items = append(items, kv{area, n})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].n != items[j].n {
			return items[i].n > items[j].n
		}
		return items[i].area < items[j].area
	})
	var b strings.Builder
	for i, item := range items {
		if i >= 5 {
			b.WriteString("…")
			break
		}
		if i > 0 {
			b.WriteString("、")
		}
		b.WriteString(fmt.Sprintf("%s×%d", item.area, item.n))
	}
	return b.String()
}
