// Command benchscore 把一次 SRC 任务的发现与金标对照，算 precision/recall/F1、
// 等级一致性与覆盖率/成本指标。
//
// 用法：
//
//	go run ./srcbench/score --target local-vuln \
//	  --golden srcbench/targets.json --findings findings.json \
//	  --src src.json --tokens 123456
//
// findings.json 可以是发现列表的 JSON 数组，或 {"items":[...]}（发现页 API 的形状）。
// 每条至少给 vulnclass / severity / status；端点从 endpoints[] 或 assets[].label 推导。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/lin9527ya/bladepro/report"
)

type Golden struct {
	VulnClass string `json:"vulnclass"`
	Endpoint  string `json:"endpoint"`
	Severity  string `json:"severity,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

type Target struct {
	Name    string         `json:"name"`
	BaseURL string         `json:"base_url"`
	Mode    string         `json:"mode,omitempty"`
	Budget  map[string]any `json:"budget,omitempty"`
	Golden  []Golden       `json:"golden"`
}

type AssetRef struct {
	Label string `json:"label"`
}

type Finding struct {
	ID        json.RawMessage `json:"id,omitempty"`
	FindingID json.RawMessage `json:"finding_id,omitempty"`
	Title     string          `json:"title,omitempty"`
	Name      string          `json:"name,omitempty"`
	VulnClass string          `json:"vulnclass"`
	Severity  string          `json:"severity"`
	Status    string          `json:"status,omitempty"`
	Endpoints []string        `json:"endpoints,omitempty"`
	Assets    []AssetRef      `json:"assets,omitempty"`
}

type SrcSummary struct {
	CellsTotal    int `json:"cells_total"`
	CellsTerminal int `json:"cells_terminal"`
	ChecksTotal   int `json:"checks_total"`
	ChecksDone    int `json:"checks_done"`
	GapsTotal     int `json:"gaps_total"`
}

type Match struct {
	Golden  Golden `json:"golden"`
	Finding string `json:"finding"`
	Title   string `json:"title,omitempty"`
	// SeverityOK 表示这条命中的等级与金标一致（金标没写等级时为 nil）。
	SeverityOK *bool `json:"severity_ok,omitempty"`
}

type Result struct {
	Target    string   `json:"target"`
	TP        int      `json:"tp"`
	FP        int      `json:"fp"`
	FN        int      `json:"fn"`
	Precision float64  `json:"precision"`
	Recall    float64  `json:"recall"`
	F1        float64  `json:"f1"`
	Severity  int      `json:"severity_matches"`
	SeverityN int      `json:"severity_checked"`
	Matches   []Match  `json:"matches,omitempty"`
	Missed    []Golden `json:"missed,omitempty"`
	FalseHits []string `json:"false_positives,omitempty"`

	CellsTotal    int     `json:"cells_total,omitempty"`
	CellsTerminal int     `json:"cells_terminal,omitempty"`
	ChecksTotal   int     `json:"checks_total,omitempty"`
	ChecksDone    int     `json:"checks_done,omitempty"`
	GapsTotal     int     `json:"gaps_total,omitempty"`
	CoverageRate  float64 `json:"coverage_rate,omitempty"`
	CheckRate     float64 `json:"check_rate,omitempty"`
	Tokens        int64   `json:"tokens,omitempty"`
	TokensPerTP   float64 `json:"tokens_per_tp,omitempty"`
}

var excludedStatuses = map[string]bool{
	"false_positive": true, "duplicate": true, "retracted": true,
	"ruled_out": true, "ignored": true, "risk_accepted": true,
}

// score 是纯判定核：每条金标最多匹配一条未使用的发现；未匹配的发现算 FP。
func score(t Target, findings []Finding) Result {
	res := Result{Target: t.Name}
	used := make([]bool, len(findings))
	for _, g := range t.Golden {
		gClass := report.CanonicalVulnClass(g.VulnClass)
		gEp := normalizeGoldenEndpoint(t.BaseURL, g.Endpoint)
		matched := -1
		for i, f := range findings {
			if used[i] || excludedStatuses[strings.ToLower(strings.TrimSpace(f.Status))] {
				continue
			}
			if report.CanonicalVulnClass(f.VulnClass) != gClass {
				continue
			}
			if !findingMatchesEndpoint(f, gEp, t.BaseURL) {
				continue
			}
			matched = i
			break
		}
		if matched < 0 {
			res.FN++
			res.Missed = append(res.Missed, g)
			continue
		}
		used[matched] = true
		res.TP++
		m := Match{Golden: g, Finding: findingLabel(findings[matched]), Title: firstNonEmpty(findings[matched].Title, findings[matched].Name)}
		if strings.TrimSpace(g.Severity) != "" {
			ok := strings.EqualFold(strings.TrimSpace(findings[matched].Severity), strings.TrimSpace(g.Severity))
			m.SeverityOK = &ok
			res.SeverityN++
			if ok {
				res.Severity++
			}
		}
		res.Matches = append(res.Matches, m)
	}
	for i, f := range findings {
		if used[i] || excludedStatuses[strings.ToLower(strings.TrimSpace(f.Status))] {
			continue
		}
		res.FP++
		res.FalseHits = append(res.FalseHits, findingLabel(f)+" · "+firstNonEmpty(f.VulnClass, "—"))
	}
	res.Precision = ratio(res.TP, res.TP+res.FP)
	res.Recall = ratio(res.TP, res.TP+res.FN)
	if res.Precision+res.Recall > 0 {
		res.F1 = 2 * res.Precision * res.Recall / (res.Precision + res.Recall)
	}
	return res
}

func applySummary(res *Result, s SrcSummary, tokens int64) {
	res.CellsTotal, res.CellsTerminal = s.CellsTotal, s.CellsTerminal
	res.ChecksTotal, res.ChecksDone = s.ChecksTotal, s.ChecksDone
	res.GapsTotal = s.GapsTotal
	res.CoverageRate = ratio(s.CellsTerminal, s.CellsTotal)
	res.CheckRate = ratio(s.ChecksDone, s.ChecksTotal)
	res.Tokens = tokens
	if tokens > 0 && res.TP > 0 {
		res.TokensPerTP = float64(tokens) / float64(res.TP)
	}
}

func ratio(a, b int) float64 {
	if b <= 0 {
		return 0
	}
	return float64(a) / float64(b)
}

// normalizeGoldenEndpoint 把 "GET /api/users/{id}" 或完整 URL 归一成
// report.NormalizeEndpoint 的字符串（相对路径按 base URL 补主机）。
func normalizeGoldenEndpoint(base, raw string) string {
	method, ep := splitMethodEndpoint(raw)
	if strings.HasPrefix(ep, "/") {
		if strings.TrimSpace(base) == "" {
			base = "http://bench.local"
		}
		ep = strings.TrimRight(base, "/") + ep
	}
	return report.NormalizeEndpoint(method, ep)
}

func splitMethodEndpoint(raw string) (method, endpoint string) {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, ' '); i > 0 {
		if m := strings.ToUpper(raw[:i]); m == "GET" || m == "POST" || m == "PUT" || m == "PATCH" || m == "DELETE" || m == "OPTIONS" || m == "HEAD" {
			return m, strings.TrimSpace(raw[i+1:])
		}
	}
	return "", raw
}

func findingMatchesEndpoint(f Finding, goldenNorm, base string) bool {
	for _, raw := range findingEndpoints(f) {
		method, ep := splitMethodEndpoint(raw)
		if strings.HasPrefix(ep, "/") {
			if strings.TrimSpace(base) == "" {
				base = "http://bench.local"
			}
			ep = strings.TrimRight(base, "/") + ep
		}
		norm := report.NormalizeEndpoint(method, ep)
		if norm == goldenNorm {
			return true
		}
		// 放宽：同一 host+路径，忽略方法/查询键（agent 常少写/多写参数）。
		if samePath(norm, goldenNorm) {
			return true
		}
	}
	return false
}

func findingEndpoints(f Finding) []string {
	out := append([]string(nil), f.Endpoints...)
	for _, a := range f.Assets {
		if label := strings.TrimSpace(a.Label); label != "" {
			out = append(out, label)
		}
	}
	return out
}

func samePath(a, b string) bool {
	pa, oka := normPath(a)
	pb, okb := normPath(b)
	return oka && okb && pa != "" && pa == pb
}

func normPath(norm string) (string, bool) {
	// norm 形如 "GET host/path?k1,k2"（无 scheme）。
	fields := strings.Fields(norm)
	if len(fields) == 0 {
		return "", false
	}
	raw := fields[len(fields)-1]
	u, err := url.Parse("http://" + raw)
	if err != nil {
		return "", false
	}
	return strings.ToLower(u.Hostname()) + strings.TrimRight(u.Path, "/"), true
}

func findingLabel(f Finding) string {
	if id := firstNonEmpty(string(f.FindingID), string(f.ID)); id != "" && id != "null" && id != `""` {
		return "#" + strings.Trim(id, `"`)
	}
	return firstNonEmpty(f.Title, f.Name, f.VulnClass)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func loadTargets(path, name string) (Target, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Target{}, err
	}
	var targets []Target
	if err := json.Unmarshal(raw, &targets); err != nil {
		return Target{}, err
	}
	for _, t := range targets {
		if name == "" || t.Name == name {
			return t, nil
		}
	}
	return Target{}, fmt.Errorf("找不到目标 %q", name)
}

func loadFindings(path string) ([]Finding, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var arr []Finding
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}
	var wrapped struct {
		Items []Finding `json:"items"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("findings JSON 既不是数组也不是 {items:[]}: %w", err)
	}
	return wrapped.Items, nil
}

func loadSummary(path string) (SrcSummary, error) {
	if strings.TrimSpace(path) == "" {
		return SrcSummary{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return SrcSummary{}, err
	}
	var s SrcSummary
	if err := json.Unmarshal(raw, &s); err != nil {
		return SrcSummary{}, err
	}
	return s, nil
}

func renderMarkdown(res Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# SRC 金标评测 · %s\n\n", firstNonEmpty(res.Target, "target"))
	b.WriteString("| 指标 | 值 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 命中 / 误报 / 漏报 | %d / %d / %d |\n", res.TP, res.FP, res.FN)
	fmt.Fprintf(&b, "| Precision / Recall / F1 | %.2f / %.2f / %.2f |\n", res.Precision, res.Recall, res.F1)
	if res.SeverityN > 0 {
		fmt.Fprintf(&b, "| 等级一致 | %d/%d |\n", res.Severity, res.SeverityN)
	}
	if res.CellsTotal > 0 {
		fmt.Fprintf(&b, "| 覆盖格完成率 | %.1f%%（%d/%d，缺口 %d） |\n", res.CoverageRate*100, res.CellsTerminal, res.CellsTotal, res.GapsTotal)
	}
	if res.ChecksTotal > 0 {
		fmt.Fprintf(&b, "| 收尾闸完成率 | %.1f%%（%d/%d） |\n", res.CheckRate*100, res.ChecksDone, res.ChecksTotal)
	}
	if res.Tokens > 0 {
		fmt.Fprintf(&b, "| Token / 每条命中 | %d / %.0f |\n", res.Tokens, res.TokensPerTP)
	}
	if len(res.Matches) > 0 {
		b.WriteString("\n## 命中\n\n")
		for _, m := range res.Matches {
			sev := ""
			if m.SeverityOK != nil {
				if *m.SeverityOK {
					sev = "，等级一致"
				} else {
					sev = "，等级不一致"
				}
			}
			fmt.Fprintf(&b, "- %s %s → %s %s%s\n", m.Golden.Endpoint, m.Golden.VulnClass, m.Finding, m.Title, sev)
		}
	}
	if len(res.Missed) > 0 {
		b.WriteString("\n## 漏报\n\n")
		for _, g := range res.Missed {
			fmt.Fprintf(&b, "- %s %s（期望 %s）\n", g.Endpoint, g.VulnClass, g.Severity)
		}
	}
	if len(res.FalseHits) > 0 {
		sort.Strings(res.FalseHits)
		b.WriteString("\n## 误报（未命中任何金标）\n\n")
		for _, f := range res.FalseHits {
			fmt.Fprintf(&b, "- %s\n", f)
		}
	}
	return b.String()
}

func main() {
	goldenPath := flag.String("golden", "srcbench/targets.json", "金标清单 JSON")
	targetName := flag.String("target", "", "目标名（默认第一个）")
	findingsPath := flag.String("findings", "", "发现 JSON（数组或 {items:[]}）")
	srcPath := flag.String("src", "", "可选：任务 /src 快照里的 summary JSON")
	tokens := flag.Int64("tokens", 0, "可选：任务 token 总量")
	asJSON := flag.Bool("json", false, "输出 JSON 而不是 Markdown")
	flag.Parse()
	if strings.TrimSpace(*findingsPath) == "" {
		fmt.Fprintln(os.Stderr, "缺少 --findings")
		os.Exit(2)
	}
	target, err := loadTargets(*goldenPath, *targetName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	findings, err := loadFindings(*findingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	summary, err := loadSummary(*srcPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	res := score(target, findings)
	applySummary(&res, summary, *tokens)
	if *asJSON {
		raw, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(raw))
		return
	}
	fmt.Print(renderMarkdown(res))
}
