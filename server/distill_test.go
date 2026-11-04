package server

import (
	"strings"
	"testing"
)

// TestExtractJSONObject 覆盖模型输出的三种常见形态：纯 JSON、```json 围栏、前后带解释文字。
func TestExtractJSONObject(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", `{"title":"t","summary":"s"}`, "t"},
		{"fenced", "这是结果：\n```json\n{\"title\":\"n\",\"summary\":\"s\"}\n```\n完成", "n"},
		{"prose", `好的，蒸馏如下 {"title":"p","summary":"s"} 以上。`, "p"},
	}
	for _, c := range cases {
		out, err := extractJSONObject(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got, _ := out["title"].(string); got != c.want {
			t.Fatalf("%s: title=%q want %q", c.name, got, c.want)
		}
	}
	if _, err := extractJSONObject("没有 JSON"); err == nil {
		t.Fatal("无 JSON 时应报错")
	}
	if _, err := extractJSONObject(`{bad json}`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// TestDistillClip 按字符截断（不切坏中文）。
func TestDistillClip(t *testing.T) {
	if got := distillClip("中文测试内容", 2); got != "中文…" {
		t.Fatalf("clip=%q", got)
	}
	if got := distillClip("短", 10); got != "短" {
		t.Fatalf("clip=%q", got)
	}
}

// TestDistillJSONWithoutProfile 没有激活 profile 时返回 errNoLLM（调用方据此回退）。
func TestDistillJSONWithoutProfile(t *testing.T) {
	s := &Server{}
	if _, err := s.distillJSON(t.Context(), "sys", "user"); err == nil || !strings.Contains(err.Error(), "未配置") {
		t.Fatalf("期望未配置错误，实际 %v", err)
	}
}
