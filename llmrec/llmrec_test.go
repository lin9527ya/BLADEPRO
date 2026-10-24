package llmrec

import (
	"context"
	"iter"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/lin9527ya/Norma/llm"
	"github.com/lin9527ya/Norma/transcript"
	"github.com/lin9527ya/bladepro/db"
)

type completeProvider struct{}

func (completeProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(func(llm.StreamEvent, error) bool) {}
}

func (completeProvider) Complete(context.Context, llm.CompletionRequest) (llm.Message, string, llm.Usage, error) {
	return llm.Message{
		Role: llm.RoleAssistant,
		Content: []llm.ContentBlock{
			{Type: llm.BlockThinking, Thinking: "reasoning"},
			llm.TextBlock("answer"),
		},
	}, "stop", llm.Usage{InputTokens: 7, OutputTokens: 3}, nil
}

func TestTaskIDContextUsesExplicitRegistryID(t *testing.T) {
	ctx := WithTaskID(context.Background(), " 42 ")
	if got := TaskIDFrom(ctx); got != "42" {
		t.Fatalf("TaskIDFrom()=%q want 42", got)
	}
	if got := TaskIDFrom(WithTaskID(ctx, "   ")); got != "42" {
		t.Fatalf("blank task id should preserve parent context, got %q", got)
	}
	//lint:ignore SA1012 本用例专门验证 nil ctx 的防御分支（llmrec.go 里 TaskIDFrom 显式判 nil）
	if got := TaskIDFrom(nil); got != "" {
		t.Fatalf("nil context returned %q", got)
	}
}

func TestParseSessionFallbackIsExplorationScoped(t *testing.T) {
	taskID, worker := parseSession("exp12-worker-i99")
	if taskID != "12" || worker != "worker" {
		t.Fatalf("parseSession()=(%q,%q)", taskID, worker)
	}
	if taskID, worker := parseSession("not-a-task"); taskID != "" || worker != "" {
		t.Fatalf("unexpected non-session parse: (%q,%q)", taskID, worker)
	}
}

func TestCompleteForwardsAtomicResponse(t *testing.T) {
	recorder := Wrap(completeProvider{}, nil, "model", "profile", "", "", func() bool { return false })
	msg, stopReason, usage, err := recorder.Complete(context.Background(), llm.CompletionRequest{})
	if err != nil {
		t.Fatalf("Complete() error: %v", err)
	}
	if got := msg.Text(); got != "answer" {
		t.Fatalf("Complete() text=%q want answer", got)
	}
	if stopReason != "stop" {
		t.Fatalf("Complete() stop reason=%q want stop", stopReason)
	}
	if usage.InputTokens != 7 || usage.OutputTokens != 3 {
		t.Fatalf("Complete() usage=%+v", usage)
	}
}

type meteredStreamProvider struct{ completeProvider }

func (meteredStreamProvider) Stream(context.Context, llm.CompletionRequest) iter.Seq2[llm.StreamEvent, error] {
	return func(yield func(llm.StreamEvent, error) bool) {
		if !yield(llm.StreamEvent{Type: llm.SEMessageStart, Usage: llm.Usage{InputTokens: 23}}, nil) {
			return
		}
		if !yield(llm.StreamEvent{Type: llm.SEMessageDelta, Usage: llm.Usage{OutputTokens: 5}}, nil) {
			return
		}
		yield(llm.StreamEvent{Type: llm.SEMessageStop}, nil)
	}
}

func TestSideUsageRecordedOnceOnConsumerCancellation(t *testing.T) {
	dsn := os.Getenv("BLADEPRO_PG_DSN")
	if dsn == "" {
		t.Skip("requires isolated BLADEPRO_PG_DSN")
	}
	pg, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pg.Close()
	for _, early := range []bool{false, true} {
		profile := "btw-metering-" + uuid.NewString()
		ctx, cancel := context.WithCancel(transcript.WithSessionID(t.Context(), "exp0-btw-test"))
		recorder := Wrap(meteredStreamProvider{}, pg, "fixture", profile, "", "", func() bool { return false })
		for event, err := range recorder.Stream(ctx, llm.CompletionRequest{}) {
			if err != nil {
				t.Fatal(err)
			}
			if early && event.Type == llm.SEMessageDelta {
				cancel()
				break // Consumer exits before the provider can return a cancellation event.
			}
		}
		cancel()
		var count, input, output int
		var worker, status string
		err = pg.QueryRow(`SELECT count(*),max(input_tokens),max(output_tokens),max(worker),max(status)
FROM llm_usage WHERE profile_name=$1`, profile).Scan(&count, &input, &output, &worker, &status)
		_, _ = pg.Exec(`DELETE FROM llm_usage WHERE profile_name=$1`, profile)
		if err != nil {
			t.Fatal(err)
		}
		wantStatus := "ok"
		if early {
			wantStatus = "error"
		}
		if count != 1 || input != 23 || output != 5 || worker != "btw" || status != wantStatus {
			t.Fatalf("early=%v: count=%d usage=%d/%d worker=%q status=%q", early, count, input, output, worker, status)
		}
	}
}

func TestPricesCost(t *testing.T) {
	p := Prices{InputPerMTok: 3, OutputPerMTok: 15, CacheReadPerMTok: 0.3, CacheWritePerMTok: 3.75}
	// InputTokens 1M 全是未缓存输入；2M 的情况里 1M 是缓存读（不应再按输入价计）。
	got := p.Cost(llm.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000,
		CacheReadTokens: 1_000_000, CacheWriteTokens: 1_000_000})
	want := 15.0 + 0.3 + 3.75 // uncached input clamped to 0
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Cost()=%v want %v", got, want)
	}
	got = p.Cost(llm.Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000})
	want = 3.0 + 15.0 + 0.3
	if diff := got - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("Cost()=%v want %v", got, want)
	}
	// 未配置单价 = 成本 0，而不是凭猜测填一个值。
	if got := (Prices{}).Cost(llm.Usage{InputTokens: 500, OutputTokens: 500}); got != 0 {
		t.Fatalf("zero prices Cost()=%v want 0", got)
	}
}

func TestIntentContext(t *testing.T) {
	ctx := WithIntent(context.Background(), 42)
	if got := IntentFrom(ctx); got != 42 {
		t.Fatalf("IntentFrom()=%d want 42", got)
	}
	// 0/负数表示"不归属意图"，不应覆盖上层上下文。
	if got := IntentFrom(WithIntent(ctx, 0)); got != 42 {
		t.Fatalf("zero intent overwrote parent: %d", got)
	}
	if got := IntentFrom(context.Background()); got != 0 {
		t.Fatalf("background IntentFrom()=%d want 0", got)
	}
}
