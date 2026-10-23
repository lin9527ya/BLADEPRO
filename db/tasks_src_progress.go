package db

import (
	"context"
	"fmt"
)

// SRC 进度冗余写回。
//
// 为什么冗余：收尾门每轮都要算一次全量覆盖（assets × 面 × 收尾项），但那是一次性的
// 写路径；而任务列表页要展示"面 x/y、闸 x/y、还剩几个缺口"——如果列表页去逐任务算，
// 就是 N+1 查询 + 每任务一遍 CTE。把最近一次判定的结果写回 tasks，读路径就是零成本。
//
// 语义：这些数字是**最近一次收尾门判定**的快照，不是实时值；任务没再跑过时可能滞后。
type SrcProgress struct {
	CellsTotal  int
	CellsDone   int
	ChecksTotal int
	ChecksDone  int
	GapsTotal   int
}

// SetTaskSrcProgress 写回一次收尾门判定结果（只更新 5 个计数字段）。
func (d *DB) SetTaskSrcProgress(ctx context.Context, taskID int64, p SrcProgress) error {
	if d == nil || d.DB == nil {
		return fmt.Errorf("database unavailable")
	}
	if taskID <= 0 {
		return fmt.Errorf("invalid task id")
	}
	_, err := d.ExecContext(ctx, `
UPDATE tasks SET
    src_cells_total  = $2,
    src_cells_done   = $3,
    src_checks_total = $4,
    src_checks_done  = $5,
    src_gaps_total   = $6
WHERE id = $1`,
		taskID, p.CellsTotal, p.CellsDone, p.ChecksTotal, p.ChecksDone, p.GapsTotal)
	return err
}
