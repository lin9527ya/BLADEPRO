package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/srcmode"
)

var errSrcIncomplete = errors.New("SRC proof incomplete")

func (e *Engine) completeSrcTask(ctx context.Context, t *Task) (bool, error) {
	id, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return false, err
	}
	e.m.taskStateMu.Lock()

	var rep srcmode.Report
	won, err := e.m.pg.CompleteSrcTask(ctx, id, func(view *db.DB, task *db.Task) (json.RawMessage, error) {
		var err error
		rep, err = srcmode.CheckFinish(ctx, view.Src(), id, srcmode.Parse(task.ModeDoc))
		if err != nil {
			return nil, err
		}
		var goals, active int
		if err := view.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE kind='goal' AND state='open'),count(*) FILTER(WHERE kind='intent' AND state IN ('open','running','paused')) FROM exploration_nodes WHERE exploration_id=$1`, task.ExplorationID).Scan(&goals, &active); err != nil {
			return nil, err
		}
		if goals > 0 || active > 0 {
			rep.Finishable = false
			rep.GapsTotal++
			rep.Gaps = append(rep.Gaps, srcmode.Gap{Area: "workflow", Item: "pending", Kind: srcmode.GapBlank, Reason: fmt.Sprintf("仍有 %d 个未完成目标和 %d 个未收束意图", goals, active)})
			rep.GapsByArea["workflow"]++
		}
		if !rep.Finishable {
			return nil, errSrcIncomplete
		}
		assets, err := view.Src().InScopeSrcAssets(ctx, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"coverage": rep, "asset_snapshot": assets})
	})
	if won {
		e.m.mu.Lock()
		if current := e.m.tasks[t.ID]; current != nil {
			current.updateLifecycle(func(state *taskLifecycleState) { state.Status = "done"; state.CompletedAt = time.Now().Unix() })
		}
		e.m.mu.Unlock()
		if tr := e.m.Traffic(); tr != nil {
			tr.ClearTaskQPS(id)
		}
	}
	e.m.taskStateMu.Unlock()
	if errors.Is(err, errSrcIncomplete) {
		e.refuseSrcFinish(t, rep)
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if won {
		e.resetSrcGate(t.ID)
	}
	return won, nil
}
