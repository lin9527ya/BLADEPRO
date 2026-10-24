package report

// CVSS 3.x base score + CWE 归一（P1 影响面/定级建议）。
//
// 只做**建议**：机器算分、指出与自报 severity 是否一致，不替人裁决。评分口径
// 严格按 CVSS 3.1 规范（base metrics），Temporal/Environmental 不参与。

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

var cwePattern = regexp.MustCompile(`(?i)^cwe-(\d{1,5})$`)

// NormalizeCWEList 校验并归一 CWE 列表（逗号分隔，允许为空）。返回规范化后的
// "CWE-639,CWE-284"；任一非法项整条拒绝（宁可让模型改对，也别把错分类写进库）。
func NormalizeCWEList(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		m := cwePattern.FindStringSubmatch(part)
		if m == nil {
			return "", fmt.Errorf("CWE 格式应为 CWE-<数字>：%q", part)
		}
		id := "CWE-" + m[1]
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return strings.Join(out, ","), nil
}

// CVSS31BaseScore 解析 CVSS:3.0/3.1 向量并计算 base score。返回分数与归一化向量。
func CVSS31BaseScore(vector string) (float64, string, error) {
	raw := strings.TrimSpace(vector)
	if raw == "" {
		return 0, "", fmt.Errorf("CVSS 向量为空")
	}
	parts := strings.Split(raw, "/")
	prefix := strings.ToUpper(strings.TrimSpace(parts[0]))
	if prefix != "CVSS:3.1" && prefix != "CVSS:3.0" {
		return 0, "", fmt.Errorf("只支持 CVSS:3.0 / CVSS:3.1 向量：%q", parts[0])
	}
	m := map[string]string{}
	for _, p := range parts[1:] {
		k, v, ok := strings.Cut(p, ":")
		if !ok {
			return 0, "", fmt.Errorf("CVSS 向量段格式不对：%q", p)
		}
		m[strings.ToUpper(strings.TrimSpace(k))] = strings.ToUpper(strings.TrimSpace(v))
	}
	required := []string{"AV", "AC", "PR", "UI", "S", "C", "I", "A"}
	for _, k := range required {
		if m[k] == "" {
			return 0, "", fmt.Errorf("CVSS 向量缺基础指标 %s", k)
		}
	}
	avW := map[string]float64{"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2}
	acW := map[string]float64{"L": 0.77, "H": 0.44}
	uiW := map[string]float64{"N": 0.85, "R": 0.62}
	ciaW := map[string]float64{"H": 0.56, "L": 0.22, "N": 0}
	scope := m["S"]
	if scope != "U" && scope != "C" {
		return 0, "", fmt.Errorf("CVSS S 只能是 U/C：%q", scope)
	}
	av, ok1 := avW[m["AV"]]
	ac, ok2 := acW[m["AC"]]
	ui, ok3 := uiW[m["UI"]]
	c, ok4 := ciaW[m["C"]]
	i, ok5 := ciaW[m["I"]]
	a, ok6 := ciaW[m["A"]]
	if !(ok1 && ok2 && ok3 && ok4 && ok5 && ok6) {
		return 0, "", fmt.Errorf("CVSS 向量含非法指标值：%s", raw)
	}
	prW := map[string]float64{"N": 0.85, "L": 0.62, "H": 0.27}
	if scope == "C" {
		prW = map[string]float64{"N": 0.85, "L": 0.68, "H": 0.5}
	}
	pr, ok := prW[m["PR"]]
	if !ok {
		return 0, "", fmt.Errorf("CVSS PR 只能是 N/L/H：%q", m["PR"])
	}

	iss := 1 - (1-c)*(1-i)*(1-a)
	var impact float64
	if scope == "U" {
		impact = 6.42 * iss
	} else {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	exploitability := 8.22 * av * ac * pr * ui
	score := 0.0
	if impact > 0 {
		if scope == "U" {
			score = roundUp1(math.Min(impact+exploitability, 10))
		} else {
			score = roundUp1(math.Min(1.08*(impact+exploitability), 10))
		}
	}
	return score, fmt.Sprintf("CVSS:3.1/AV:%s/AC:%s/PR:%s/UI:%s/S:%s/C:%s/I:%s/A:%s",
		m["AV"], m["AC"], m["PR"], m["UI"], m["S"], m["C"], m["I"], m["A"]), nil
}

func roundUp1(x float64) float64 {
	return math.Ceil(x*10-1e-9) / 10
}

// CVSSSeverity 把分数映射到内部四档（0-3.9 → low）。仅用于一致性提示。
func CVSSSeverity(score float64) string {
	switch {
	case score >= 9.0:
		return "critical"
	case score >= 7.0:
		return "high"
	case score >= 4.0:
		return "medium"
	default:
		return "low"
	}
}

// CVSSSeverityAgrees 判断自报等级与向量分数是否落在同一档。
func CVSSSeverityAgrees(severity string, score float64) bool {
	return strings.ToLower(strings.TrimSpace(severity)) == CVSSSeverity(score)
}

// CVSSScoreLabel 给 UI/报告一行式展示。
func CVSSScoreLabel(score float64) string {
	label := map[string]string{"critical": "严重", "high": "高危", "medium": "中危", "low": "低危"}[CVSSSeverity(score)]
	return fmt.Sprintf("%.1f（%s）", score, label)
}

// ParseCVSSScoreOrZero 是给历史数据/展示用的宽松入口：解析失败返回 0,false。
func ParseCVSSScoreOrZero(vector string) (float64, bool) {
	score, _, err := CVSS31BaseScore(vector)
	if err != nil {
		return 0, false
	}
	return score, true
}
