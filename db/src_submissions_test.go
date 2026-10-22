package db

import (
	"context"
		"testing"
)

// TestSrcSubmissionStateMachine 钉住台账状态机的合法边：台账是审计记录，不能随便回退。
func TestSrcSubmissionStateMachine(t *testing.T) {
	legal := [][2]string{
		{SrcSubDraft, SrcSubReady},
		{SrcSubReady, SrcSubSubmitted},
		{SrcSubSubmitted, SrcSubAccepted},
		{SrcSubSubmitted, SrcSubDuplicate},
		{SrcSubSubmitted, SrcSubRejected},
		{SrcSubAccepted, SrcSubPaid},
		{SrcSubAccepted, SrcSubWithdrawn},
		{SrcSubRejected, SrcSubWithdrawn},
		{SrcSubDraft, SrcSubWithdrawn},
	}
	for _, e := range legal {
		if err := CanTransitionSrcSubmission(e[0], e[1]); err != nil {
			t.Fatalf("%s → %s 应当放行：%v", e[0], e[1], err)
		}
	}
	illegal := [][2]string{
		{SrcSubDraft, SrcSubSubmitted}, // 跳过 ready：交出去之前必须先齐备
		{SrcSubDraft, SrcSubAccepted},  // 没交就有结论
		{SrcSubReady, SrcSubAccepted},
		{SrcSubPaid, SrcSubDraft}, // 到账还能改回草稿？
		{SrcSubWithdrawn, SrcSubSubmitted},
		{SrcSubDuplicate, SrcSubAccepted},
	}
	for _, e := range illegal {
		if err := CanTransitionSrcSubmission(e[0], e[1]); err == nil {
			t.Fatalf("%s → %s 必须被拒绝", e[0], e[1])
		}
	}
	if err := CanTransitionSrcSubmission(SrcSubDraft, "hacked"); err == nil {
		t.Fatal("未知状态必须被拒绝")
	}
	// 不存在的行（from=""）允许任意合法状态：创建时就要能写 ready。
	if err := CanTransitionSrcSubmission("", SrcSubReady); err != nil {
		t.Fatalf("新建行应当允许直接 ready：%v", err)
	}
}

// TestSrcSubmissionLedgerRoundTrip 走真实 PG 的台账闭环（无库自动跳过）：
// 准备 → 列表 → 提交（必须有 external_id）→ 受理 → 到账。
func TestSrcSubmissionLedgerRoundTrip(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skipf("no postgres configured (%v) — skipping", err)
	}
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	task, err := d.CreateTaskWithOptions("src-ledger-test", "台账闭环", TaskCreateOptions{})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	findingID, err := d.AddFinding(task.ID, 0, "authz", "越权读取订单", "high",
		"跨租户读取订单", "", "test", nil)
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	var expID int64
	if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task.ID).Scan(&expID); err != nil {
		t.Fatalf("exploration: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM src_submissions WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM src_finding_meta WHERE task_id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM findings WHERE id=$1`, findingID)
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})

	store := d.Src()
	ctx := context.Background()
	sub, meta, err := store.PrepareSrcSubmission(ctx, SrcSubmissionInput{
		TaskID: task.ID, FindingID: findingID, Title: "越权读取订单", Severity: "high",
		Fingerprint: "0123456789abcdef", Description: "我用自己的会话读到了别人的订单",
		Impact: "可读取全部租户订单", Fix: "按 tenant_id 校验归属",
		Status: SrcSubDraft, ReportReady: false,
		Compliance: []byte(`{"ok":false,"issues":["no_proof"]}`),
	})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if sub == nil || sub.Status != SrcSubDraft || meta == nil || meta.ReportReady {
		t.Fatalf("准备结果不对：sub=%+v meta=%+v", sub, meta)
	}

	// 重复准备只更新正文，不新增行。
	again, _, err := store.PrepareSrcSubmission(ctx, SrcSubmissionInput{
		TaskID: task.ID, FindingID: findingID, Title: "越权读取订单", Severity: "high",
		Description: "补齐后的描述：我用自己的会话读到了别人租户的订单详情",
		Impact:      "可读取全部租户订单", Fix: "按 tenant_id 校验归属", Status: SrcSubReady, ReportReady: true,
	})
	if err != nil {
		t.Fatalf("re-prepare: %v", err)
	}
	if again.ID != sub.ID {
		t.Fatalf("同一 (finding, platform) 必须复用一行：%d vs %d", again.ID, sub.ID)
	}
	if again.Status != SrcSubReady {
		t.Fatalf("重新准备应把 draft 推到 ready，got %s", again.Status)
	}

	// submitted 必须有平台编号。
	if _, err := store.UpdateSrcSubmission(ctx, sub.ID, SrcSubmissionPatch{Status: ptrStr(SrcSubSubmitted)}); err == nil {
		t.Fatal("submitted 没有 external_id 必须被拒绝")
	}
	submitted, err := store.UpdateSrcSubmission(ctx, sub.ID, SrcSubmissionPatch{
		Status: ptrStr(SrcSubSubmitted), ExternalID: ptrStr("SRC-2026-0001"),
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if submitted.SubmittedAt == nil {
		t.Fatal("submitted 应当盖 submitted_at")
	}
	accepted, err := store.UpdateSrcSubmission(ctx, sub.ID, SrcSubmissionPatch{Status: ptrStr(SrcSubAccepted)})
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if accepted.VerdictAt == nil {
		t.Fatal("accepted 应当盖 verdict_at")
	}

	// 重新准备不许把已交出去的行拉回 draft。
	_, _, err = store.PrepareSrcSubmission(ctx, SrcSubmissionInput{
		TaskID: task.ID, FindingID: findingID, Title: "越权读取订单", Severity: "high",
		Description: "又改了一版描述：跨租户读取订单详情", Impact: "可读取全部租户订单",
		Fix: "按 tenant_id 校验归属", Status: SrcSubDraft, ReportReady: true,
	})
	if err != nil {
		t.Fatalf("re-prepare after submit: %v", err)
	}
	cur, err := store.GetSrcSubmission(ctx, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.Status != SrcSubAccepted {
		t.Fatalf("已受理的台账被改回 %s", cur.Status)
	}

	// 台账列表 + 交付判定视图。
	rows, err := store.ListSrcSubmissions(ctx, SrcSubmissionFilter{TaskID: task.ID})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list submissions: %v rows=%d", err, len(rows))
	}
	delivery, err := store.SrcDeliveryFindings(ctx, task.ID)
	if err != nil || len(delivery) != 1 {
		t.Fatalf("delivery rows: %v %d", err, len(delivery))
	}
	if !delivery[0].ReportReady || delivery[0].SubmissionStatus != SrcSubAccepted {
		t.Fatalf("交付视图没反映台账状态：%+v", delivery[0])
	}

	// 状态机拒绝非法回退。
	if _, err := store.UpdateSrcSubmission(ctx, sub.ID, SrcSubmissionPatch{Status: ptrStr(SrcSubDraft)}); err == nil {
		t.Fatal("accepted 不能直接回 draft")
	}
}

func ptrStr(s string) *string { return &s }

// TestSrcHostInScope 覆盖范围判定的三种结果：在范围内 / 明确越界 / 判定不了。
func TestSrcHostInScope(t *testing.T) {
	dsn, _, err := DSN()
	if err != nil {
		t.Skipf("no postgres configured (%v) — skipping", err)
	}
	d, err := Open(dsn)
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { d.Close() })

	task, err := d.CreateTask("src-scope-test", "范围判定", nil, 0, 0)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	var expID int64
	if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task.ID).Scan(&expID); err != nil {
		t.Fatalf("exploration: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task.ID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, expID)
	})
	if _, err := d.Assets().AddAgentScope(task.ID, "root_domain", "corp.example", "测试范围", "agent"); err != nil {
		t.Fatalf("add scope: %v", err)
	}
	store := d.Src()
	ctx := context.Background()

	if in, decidable, err := store.SrcHostInScope(ctx, task.ID, "api.corp.example"); err != nil || !in || !decidable {
		t.Fatalf("子域应在范围内：in=%v decidable=%v err=%v", in, decidable, err)
	}
	if in, decidable, err := store.SrcHostInScope(ctx, task.ID, "evil.example.net"); err != nil || in || !decidable {
		t.Fatalf("域外主机应判定为越界：in=%v decidable=%v err=%v", in, decidable, err)
	}

	// 只有 company 类 scope 时：host 判不了，不能诬告越界。
	task2, err := d.CreateTask("src-scope-test-2", "只能按公司关联", nil, 0, 0)
	if err != nil {
		t.Fatalf("create task2: %v", err)
	}
	var exp2 int64
	if err := d.QueryRow(`SELECT exploration_id FROM tasks WHERE id=$1`, task2.ID).Scan(&exp2); err != nil {
		t.Fatalf("exploration2: %v", err)
	}
	t.Cleanup(func() {
		_, _ = d.Exec(`DELETE FROM tasks WHERE id=$1`, task2.ID)
		_, _ = d.Exec(`DELETE FROM explorations WHERE id=$1`, exp2)
	})
	if _, _, err := store.TaskMode(ctx, task2.ID); err != nil {
		t.Fatalf("task mode: %v", err)
	}
	if in, decidable, err := store.SrcHostInScope(ctx, task2.ID, "anything.example"); err != nil || in || decidable {
		t.Fatalf("没有可判定的 scope 时应返回不可判定：in=%v decidable=%v err=%v", in, decidable, err)
	}
}

