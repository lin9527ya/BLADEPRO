package server

import (
	"path/filepath"
	"testing"
)

func TestSrcProbeTargetURL(t *testing.T) {
	cases := map[string]string{
		"http://127.0.0.1:18080":     "http://127.0.0.1:18080",
		"https://api.example.com/v1": "https://api.example.com/v1",
		"api.example.com":            "https://api.example.com",
		"api.example.com:8443":       "https://api.example.com:8443",
		"Admin Portal":               "",
		"ftp://example.com/file":     "",
		"":                           "",
	}
	for in, want := range cases {
		if got := srcProbeTargetURL(in); got != want {
			t.Errorf("srcProbeTargetURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestReadSrcProbeWordlist(t *testing.T) {
	t.Setenv("BLADEPRO_SKILL_DIR", filepath.Join("..", "skills"))
	words, err := readSrcProbeWordlist()
	if err != nil {
		t.Fatal(err)
	}
	if len(words) < 150 {
		t.Fatalf("路径字典条目太少：%d", len(words))
	}
	for _, w := range words[:5] {
		if w == "" || w[0] == '#' {
			t.Fatalf("字典含空行/注释：%q", w)
		}
	}
}
