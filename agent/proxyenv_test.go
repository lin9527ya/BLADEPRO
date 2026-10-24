package agent

import (
	"strings"
	"testing"
)

func TestProxyEnvEmptyIsNil(t *testing.T) {
	if env := proxyEnv("", ""); env != nil {
		t.Fatalf("proxyEnv(\"\", \"\") = %v, want nil (direct)", env)
	}
}

func TestProxyEnvSetsAllProxyForSocks5(t *testing.T) {
	// Capture-off egress path: a socks5 proxy, no MITM CA. ALL_PROXY must be set
	// (curl reads socks5 only from there), and no CA vars should appear.
	env := proxyEnv("socks5://10.0.0.1:1080", "")
	has := func(prefix string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "all_proxy="} {
		if !has(want) {
			t.Errorf("proxyEnv missing %s: %v", want, env)
		}
	}
	if has("SSL_CERT_FILE=") || has("CURL_CA_BUNDLE=") {
		t.Errorf("proxyEnv without CA must not inject CA vars: %v", env)
	}
}

func TestProxyEnvInjectsCAWhenRecording(t *testing.T) {
	env := proxyEnv("http://127.0.0.1:8788", "/data/ca.pem")
	has := func(prefix string) bool {
		for _, e := range env {
			if strings.HasPrefix(e, prefix) {
				return true
			}
		}
		return false
	}
	for _, want := range []string{"SSL_CERT_FILE=", "CURL_CA_BUNDLE=", "REQUESTS_CA_BUNDLE=", "NODE_EXTRA_CA_CERTS="} {
		if !has(want) {
			t.Errorf("proxyEnv with CA missing %s: %v", want, env)
		}
	}
}

// TestProxyEnvForTaskTagsAttribution pins the task-identity contract used by the
// recorder: only http(s) proxies get the bp-task-<id> userinfo (socks5 userinfo
// means SOCKS auth), and the env carries BLADEPRO_TASK_ID for tool-side filters.
func TestProxyEnvForTaskTagsAttribution(t *testing.T) {
	if got := ProxyURLForTask("http://127.0.0.1:8788", 42); got != "http://bp-task-42@127.0.0.1:8788" {
		t.Fatalf("ProxyURLForTask=%q", got)
	}
	if got := ProxyURLForTask("socks5://127.0.0.1:1080", 42); got != "socks5://127.0.0.1:1080" {
		t.Fatalf("socks5 URL must stay untouched, got %q", got)
	}
	env := ProxyEnvForTask("http://127.0.0.1:8788", "", 42)
	var hasID, hasTaggedProxy bool
	for _, e := range env {
		if e == "BLADEPRO_TASK_ID=42" {
			hasID = true
		}
		if strings.HasPrefix(e, "HTTP_PROXY=http://bp-task-42@") {
			hasTaggedProxy = true
		}
	}
	if !hasID || !hasTaggedProxy {
		t.Fatalf("task env incomplete: %v", env)
	}
	if env := ProxyEnvForTask("", "", 42); env == nil {
		t.Fatal("empty proxy must still expose task id env")
	}
}
