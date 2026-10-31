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
		e.srcTaskCommitted(t, id)
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

// forceCompleteSrcTask records a human override through the same task row lock,
// proof ledger and completed revision as the normal path. The override proof is
// deliberately explicit; it never turns an unknown state into a silent success.
func (e *Engine) forceCompleteSrcTask(ctx context.Context, t *Task, reason string) (bool, srcmode.Report, error) {
	id, err := strconv.ParseInt(t.ID, 10, 64)
	if err != nil {
		return false, srcmode.Report{}, err
	}
	e.m.taskStateMu.Lock()
	defer e.m.taskStateMu.Unlock()

	var rep srcmode.Report
	won, err := e.m.pg.ForceCompleteSrcTask(ctx, id, func(view *db.DB, task *db.Task) (json.RawMessage, error) {
		var err error
		rep, err = srcmode.CheckFinish(ctx, view.Src(), id, srcmode.Parse(task.ModeDoc))
		if err != nil {
			return nil, err
		}
		var goals, active int
		if err := view.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE kind='goal' AND state='open'),count(*) FILTER(WHERE kind='intent' AND state IN ('open','running','paused')) FROM exploration_nodes WHERE exploration_id=$1`, task.ExplorationID).Scan(&goals, &active); err != nil {
			return nil, err
		}
		assets, err := view.Src().InScopeSrcAssets(ctx, id)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{
			"manual_override": true,
			"reason":          reason,
			"coverage":        rep,
			"asset_snapshot":  assets,
			"pending_goals":   goals,
			"active_intents":  active,
		})
	})
	if err != nil {
		return false, rep, err
	}
	if won {
		e.srcTaskCommitted(t, id)
		e.resetSrcGate(t.ID)
	}
	return won, rep, nil
}

func (e *Engine) srcTaskCommitted(t *Task, id int64) {
	// A committed completion must not leave a long-running execution writing into a
	// finished task. Cancelling here also covers the human-override path.
	e.m.CancelTaskExecutions(t.ID, "任务已收尾")
	e.m.mu.Lock()
	if current := e.m.tasks[t.ID]; current != nil {
		current.updateLifecycle(func(state *taskLifecycleState) { state.Status = "done"; state.CompletedAt = time.Now().Unix() })
	}
	e.m.mu.Unlock()
	if tr := e.m.Traffic(); tr != nil {
		tr.ClearTaskQPS(id)
	}
}
