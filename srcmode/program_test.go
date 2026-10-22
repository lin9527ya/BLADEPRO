package srcmode

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

func boolPtr(b bool) *bool { return &b }

func regexpMatch(pattern, s string) bool {
	re, err := regexp.Compile(pattern)
	return err == nil && re.MatchString(s)
}

// at 构造"某天 hh:mm"的本地时刻（只用于时间窗判定）。
func at(t *testing.T, hhmm string) time.Time {
	t.Helper()
	var h, m int
	if _, err := fmt.Sscanf(hhmm, "%d:%d", &h, &m); err != nil {
		t.Fatalf("bad fixture %q: %v", hhmm, err)
	}
	return time.Date(2026, 10, 23, h, m, 0, 0, time.Local)
}

func TestProgramValidate(t *testing.T) {
	base := Program{Name: "某厂商 SRC", Platform: "vendor-src", TimeWindow: "09:00-18:00"}
	if err := base.Validate(); err != nil {
		t.Fatalf("合法项目被拒：%v", err)
	}
	cases := []struct {
		name string
		p    Program
		want string
	}{
		{"no name", Program{Name: " "}, "项目名"},
		{"negative rate", Program{Name: "x", RateLimit: -1}, "rate_limit"},
		{"rate too high", Program{Name: "x", RateLimit: 5000}, "rate_limit"},
		{"bad window", Program{Name: "x", TimeWindow: "9-18"}, "时间窗"},
		{"bad window hour", Program{Name: "x", TimeWindow: "25:00-26:00"}, "时间窗"},
		{"empty technique", Program{Name: "x", Rules: ProgramRules{ForbiddenTechniques: []string{" "}}}, "空项"},
		{"empty command rule", Program{Name: "x", Rules: ProgramRules{CommandRules: []ProgramRule{{Pattern: " "}}}}, "pattern"},
		{"negative budget", Program{Name: "x", Rules: ProgramRules{MaxIntentsTotal: -1}}, "额度"},
		{"unknown area", Program{Name: "x", Rules: ProgramRules{RequiredAreas: map[string][]string{"app": {"nope"}}}}, "未知风险面"},
		{"bad severity key", Program{Name: "x", SeverityMap: map[string]string{"super": "high"}}, "severity_map"},
		{"bad severity value", Program{Name: "x", SeverityMap: map[string]string{"high": "urgent"}}, "平台等级"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			err := p.Validate()
			if err == nil {
				t.Fatalf("期望报错含 %q，实际为 nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误 %q 不含 %q", err, tc.want)
			}
		})
	}
}

func TestProgramApplyToModeDocIsStrictlyTighter(t *testing.T) {
	doc := DefaultDoc()
	doc.MaxAttemptsPerCell = 3
	doc.RequiredAreas = map[string][]string{"app": {"authz"}}
	p := &Program{
		Name: "vendor", Platform: "vendor-src",
		Rules: ProgramRules{
			MaxAttemptsPerCell: 2,
			MaxIntentsPerAsset: 5,
			RequiredAreas:      map[string][]string{"app": {"authz", "injection"}},
			AllowBudgetStop:    boolPtr(true),
			SkipReportGate:     boolPtr(true),
		},
	}
	got := p.ApplyToModeDoc(doc)
	if got.Platform != "vendor-src" || got.Program != "vendor" {
		t.Fatalf("平台/项目标签没写进快照：%+v", got)
	}
	if got.MaxAttemptsPerCell != 2 || got.MaxIntentsPerAsset != 5 {
		t.Fatalf("额度没被平台规则收紧：%+v", got)
	}
	if len(got.RequiredAreas["app"]) != 2 {
		t.Fatalf("必做面应以平台为准：%v", got.RequiredAreas["app"])
	}
	if !got.AllowBudgetStop || !got.SkipReportGate {
		t.Fatalf("平台开关没生效：%+v", got)
	}
	// 原 doc 不能被改（快照语义：调用方持有的是旧值）。
	if doc.MaxAttemptsPerCell != 3 || len(doc.RequiredAreas["app"]) != 1 {
		t.Fatalf("ApplyToModeDoc 改了入参：%+v", doc)
	}
}

func TestProgramRateLimitRules(t *testing.T) {
	p := &Program{Name: "x"}
	if rules := p.RateLimitRules(); len(rules) != 0 {
		t.Fatalf("不限速时不该生成规则：%+v", rules)
	}
	p.RateLimit = 5
	rules := p.RateLimitRules()
	if len(rules) != 3 {
		t.Fatalf("限速应生成 3 条命令级规则，got %d", len(rules))
	}
	for _, r := range rules {
		if !strings.Contains(r.Label, "5 qps") {
			t.Fatalf("规则标签应带上限：%q", r.Label)
		}
	}
	// 关键：正则要能匹配到洪水开关，且不误伤普通命令。
	re := rules[1]
	if !re.Regex {
		t.Fatal("线程数规则必须是正则")
	}
	for _, bad := range []string{"nmap -t 100 1.2.3.4", "ffuf --threads=200 -w w.txt"} {
		if !regexpMatch(re.Pattern, bad) {
			t.Fatalf("应命中高并发：%q", bad)
		}
	}
	for _, ok := range []string{"curl https://a/b?id=1", "nmap -sV -t 4 1.2.3.4"} {
		if regexpMatch(re.Pattern, ok) {
			t.Fatalf("不该命中：%q", ok)
		}
	}
}

func TestProgramTimeWindow(t *testing.T) {
	p := &Program{Name: "x"}
	if !p.InWindow(at(t, "03:00")) {
		t.Fatal("空时间窗 = 不限")
	}
	p.TimeWindow = "09:00-18:00"
	if !p.InWindow(at(t, "09:00")) || !p.InWindow(at(t, "17:59")) {
		t.Fatal("窗口内应放行")
	}
	if p.InWindow(at(t, "08:59")) || p.InWindow(at(t, "18:01")) {
		t.Fatal("窗口外应拒绝")
	}
	// 多段
	p.TimeWindow = "09:00-12:00,13:00-18:00"
	if p.InWindow(at(t, "12:30")) {
		t.Fatal("午休应拒绝")
	}
	// 跨夜
	p.TimeWindow = "22:00-02:00"
	if !p.InWindow(at(t, "23:00")) || !p.InWindow(at(t, "01:30")) {
		t.Fatal("跨夜窗口两端都应放行")
	}
	if p.InWindow(at(t, "12:00")) {
		t.Fatal("跨夜窗口之外应拒绝")
	}
}

func TestProgramSeverityLevel(t *testing.T) {
	p := &Program{Name: "x"}
	if got := p.PlatformLevel("high"); got != "" {
		t.Fatalf("没配映射时返回空，got %q", got)
	}
	p.SeverityMap = map[string]string{"high": "critical"}
	if got := p.PlatformLevel("HIGH"); got != "critical" {
		t.Fatalf("映射应大小写不敏感，got %q", got)
	}
	if got := p.PlatformLevel("low"); got != "" {
		t.Fatalf("未映射的等级返回空，got %q", got)
	}
}

func TestProgramDescribe(t *testing.T) {
	p := &Program{Name: "vendor", Platform: "src", RateLimit: 3, TimeWindow: "09:00-18:00", ScopeNote: "只测 web\n不碰生产库"}
	got := p.Describe()
	for _, want := range []string{"【平台规则】", "平台：src", "项目：vendor", "3 请求/秒", "09:00-18:00", "只测 web 不碰生产库"} {
		if !strings.Contains(got, want) {
			t.Fatalf("说明里缺 %q：%s", want, got)
		}
	}
	if (&Program{}).Describe() != "" {
		t.Fatal("空项目不该生成说明")
	}
}
