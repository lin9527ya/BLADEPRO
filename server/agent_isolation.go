package server

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	actool "github.com/lin9527ya/Norma/tool"
)

// agentIso 是 agent 降权的进程级配置（一次安装、全局生效）。未启用时一切保持原行为：
// agent 与服务端同权——提示注入即控制台全权（见 deploy/README.md「信任模型」）。
type agentIsoState struct {
	enabled bool
	user    string
	uid     uint32
	gid     uint32
	home    string
	roots   []string // 文件工具允许访问的真实根（已 EvalSymlinks）
}

var agentIso agentIsoState

var fileGuard struct {
	installed bool
	roots     []string
	protected []string
}

// File tools have a default boundary even when OS privilege dropping is disabled.
func InstallFileRootGuard(dataDir, keyDir, skillDir string) error {
	roots, err := agentAllowedRoots(dataDir, skillDir, os.Getenv("BLADEPRO_AGENT_READ_ROOTS"))
	if err != nil {
		return err
	}
	key, err := filepath.Abs(keyDir)
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(key); err == nil {
		key = real
	}
	for _, root := range roots {
		if root == key || strings.HasPrefix(key, root+string(os.PathSeparator)) {
			return fmt.Errorf("文件工具允许根 %s 包含密钥/配置目录 %s", root, key)
		}
	}
	fileGuard.installed = true
	fileGuard.roots = roots
	dataRoot, err := filepath.Abs(dataDir)
	if err != nil {
		return err
	}
	dataRoot, err = filepath.EvalSymlinks(dataRoot)
	if err != nil {
		return err
	}
	fileGuard.protected = []string{filepath.Join(dataRoot, "evidence"), filepath.Join(dataRoot, "traffic")}
	actool.SetPathGuard(agentPathGuard)
	log.Printf("[file-guard] 文件工具限制在 %s（工具边界不是 OS/网络沙箱）", strings.Join(roots, "、"))
	return nil
}

// agentEnvDenyPrefixes / agentEnvDenyFragments 是子进程环境的"疑似凭据"判定。
//
// 刻意用**黑名单**而不是白名单：白名单会悄悄弄坏 agent 依赖的一堆工具变量
// （JAVA_HOME / NODE_OPTIONS / PLAYWRIGHT_BROWSERS_PATH…），而它们不是秘密。代价是
// "没被模式覆盖的新秘密"会漏——新增带凭据的环境变量时，记得把前缀补到这里。
var agentEnvDenyPrefixes = []string{
	"ANTHROPIC_", "OPENAI_", "CLAUDE_", "BLADEPRO_",
	"AWS_", "GH_", "GITHUB_", "AZURE_", "GOOGLE_", "GCP_", "HF_", "HUGGINGFACE_",
}

var agentEnvDenyFragments = []string{
	"API_KEY", "APIKEY", "ACCESS_KEY", "SECRET", "TOKEN", "PASSWORD", "PASSWD",
	"CREDENTIAL", "PRIVATE_KEY",
}

// agentEnvOverrideKeys 不继承，而是由 BLADEPRO 重写（家目录/身份/条目指向服务端用户的都换掉）。
var agentEnvOverrideKeys = map[string]bool{
	"HOME": true, "USER": true, "LOGNAME": true,
	"XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true, "XDG_DATA_HOME": true,
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "GPG_AGENT_INFO": true, "KRB5CCNAME": true,
}

// InstallAgentIsolation 按 BLADEPRO_AGENT_USER 给 agent 的**执行面**上锁：
//
//   - 命令（Bash / 后台任务 / 交互式 shell / run_execution / 自定义工具）以该用户身份运行；
//   - 子进程环境按"疑似凭据"模式清洗——LLM key、BLADEPRO_* 等不再被继承；
//   - Norma 的文件工具（Read/Write/Edit/MultiEdit/LS/Glob/Grep）被限制在
//     dataDir + skillDir + BLADEPRO_AGENT_READ_ROOTS 之内（逗号或冒号分隔）。
//
// 未设置 BLADEPRO_AGENT_USER 时什么都不做，只留一行信任模型提示——行为与之前完全一致。
// 设置后要求进程是 root（降权需要），否则**报错而不是静默**：配了却没生效比不配更危险。
func InstallAgentIsolation(dataDir, keyDir, skillDir string) error {
	spec := strings.TrimSpace(os.Getenv("BLADEPRO_AGENT_USER"))
	if spec == "" {
		log.Printf("[isolation] 未启用 agent 降权（BLADEPRO_AGENT_USER 未设置）：agent 与服务端同权，" +
			"提示注入即控制台全权。隔离做法见 deploy/README.md「信任模型」")
		return nil
	}
	// keyDir 放 jwt.key 镜像、config.json 也在它旁边。它若落在 dataDir 里面，降权后
	// agent 依然读得到——那就不用降权了。宁可拒绝启动，也不给一个"看着隔离、实则没隔"的状态。
	if d, k := filepath.Clean(dataDir), filepath.Clean(keyDir); k == d || strings.HasPrefix(k, d+string(os.PathSeparator)) {
		return fmt.Errorf("data 目录 %s 包含了密钥/配置目录 %s（jwt.key、config.json）："+
			"隔离模式要求 -data 与可执行文件目录分开（默认 data/ 子目录即可）", d, k)
	}
	uid, gid, home, err := resolveAgentAccount(spec)
	if err != nil {
		return fmt.Errorf("BLADEPRO_AGENT_USER=%q: %w", spec, err)
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("BLADEPRO_AGENT_USER=%q 需要以 root 启动才能降权（当前 euid=%d）", spec, os.Geteuid())
	}
	roots, err := agentAllowedRoots(dataDir, skillDir, os.Getenv("BLADEPRO_AGENT_READ_ROOTS"))
	if err != nil {
		return err
	}
	agentIso = agentIsoState{enabled: true, user: spec, uid: uid, gid: gid, home: home, roots: roots}

	if err := prepareAgentStateDirs(dataDir); err != nil {
		return fmt.Errorf("把 data 目录交给 agent 用户：%w", err)
	}
	actool.SetExecHook(hardenAgentCmd)
	actool.SetPathGuard(agentPathGuard)
	log.Printf("[isolation] agent 以 %s (uid=%d gid=%d, home=%s) 执行；文件工具允许访问：%s；子进程环境已按凭据模式清洗",
		spec, uid, gid, home, strings.Join(roots, "、"))
	return nil
}

// prepareAgentStateDirs 把 dataDir 这棵子树交给 agent 用户：降权后 agent 的 run 目录
// （<data>/tasks/<id>/i<n>）仍由服务端（root）创建，不改属主它连自己的产物目录都写不进去。
//
// 只动 dataDir：jwt.key 镜像与 config.json 在 baseDir（dataDir 的上一级），已经由
// InstallAgentIsolation 的前置检查排除在 dataDir 之外，这里碰不到它们。
// 用 Lchown 而不是 Chown——工作区里可能有 agent 建的软链，跟着软链 chown 会改到树外文件。
func prepareAgentStateDirs(dataDir string) error {
	// Only workspaces are writable by the agent. Owning the data root would let it
	// replace evidence/SQLite directories and forge the source before binding.
	if err := os.Lchown(dataDir, os.Geteuid(), int(agentIso.gid)); err != nil {
		return err
	}
	if err := os.Chmod(dataDir, 0o750|os.ModeSetgid); err != nil {
		return err
	}
	for _, name := range []string{"tasks", "sessions", "drafts", "noa"} {
		root := filepath.Join(dataDir, name)
		if err := os.MkdirAll(root, 0o775); err != nil {
			return err
		}
		if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := os.Lchown(p, int(agentIso.uid), int(agentIso.gid)); err != nil {
				return err
			}
			if d.IsDir() {
				return os.Chmod(p, 0o775|os.ModeSetgid)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, name := range []string{"evidence", "traffic"} {
		root := filepath.Join(dataDir, name)
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		if err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := os.Lchown(p, os.Geteuid(), int(agentIso.gid)); err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if d.IsDir() {
				mode := os.FileMode(0o700)
				if name == "traffic" && (p == root || p == filepath.Join(root, "_ca")) {
					mode = 0o750
				}
				return os.Chmod(p, mode)
			}
			mode := os.FileMode(0o600)
			if name == "traffic" && filepath.Base(p) == "mitmproxy-ca-cert.pem" {
				mode = 0o640
			}
			return os.Chmod(p, mode)
		}); err != nil {
			return err
		}
	}
	return nil
}

// hardenAgentCmd 把一条即将执行的命令收敛到 agent 身份：降权 + 环境清洗。
// Norma 的 ExecHook 与 BLADEPRO 自己的两个 spawn 点（run_execution、自定义工具）共用它；
// 隔离未启用时是 no-op。
func hardenAgentCmd(cmd *exec.Cmd) {
	if !agentIso.enabled {
		return
	}
	applyAgentCredential(cmd)
	cmd.Env = scrubAgentEnv(cmd.Env)
}

// scrubAgentEnv 丢掉疑似凭据的环境变量，并把 HOME/USER 指向 worker。
// 同名键取**最后一次出现**（append 覆盖语义；libc getenv 取第一个，不去重就会拿到旧值）。
func scrubAgentEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	val := make(map[string]string, len(env)+3)
	order := make([]string, 0, len(env)+3)
	for _, kv := range env {
		i := strings.IndexByte(kv, '=')
		if i <= 0 {
			continue
		}
		k := kv[:i]
		if agentEnvDenied(k) {
			continue
		}
		if _, ok := val[k]; !ok {
			order = append(order, k)
		}
		val[k] = kv
	}
	out := make([]string, 0, len(order)+3)
	for _, k := range order {
		out = append(out, val[k])
	}
	out = append(out, "HOME="+agentIso.home, "USER="+agentIso.user, "LOGNAME="+agentIso.user)
	return out
}

func agentEnvDenied(key string) bool {
	up := strings.ToUpper(key)
	if agentEnvOverrideKeys[up] {
		return true
	}
	for _, p := range agentEnvDenyPrefixes {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	for _, f := range agentEnvDenyFragments {
		if strings.Contains(up, f) {
			return true
		}
	}
	return false
}

// agentPathGuard 是隔离模式下文件工具的允许面：只放行配置的根（含其子路径）。
// 它收到的是 Norma 解析过软链的真实路径，所以软链绕不过去。
func agentPathGuard(abs string) error {
	if !agentIso.enabled && !fileGuard.installed {
		return nil
	}
	roots := agentIso.roots
	if fileGuard.installed {
		roots = fileGuard.roots
	}
	for _, root := range fileGuard.protected {
		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return fmt.Errorf("拒绝直接访问服务端证据存储；请使用已校验的证据/流量工具")
		}
	}
	for _, root := range roots {
		if abs == root || strings.HasPrefix(abs, root+string(os.PathSeparator)) {
			return nil
		}
	}
	return fmt.Errorf("拒绝访问 %s：隔离模式下文件工具只允许访问 %s；其它路径需要由操作者显式配置允许根",
		abs, strings.Join(roots, "、"))
}

// agentAllowedRoots 解析允许根为真实路径。dataDir / skillDir 必须存在；额外根来自
// BLADEPRO_AGENT_READ_ROOTS（PathListSeparator 或逗号分隔），写错就报错（启动期暴露）。
func agentAllowedRoots(dataDir, skillDir, extra string) ([]string, error) {
	seen := map[string]bool{}
	roots := make([]string, 0, 2)
	add := func(p string) error {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return fmt.Errorf("允许根 %s 不存在或无法解析：%w", p, err)
		}
		if seen[real] {
			return nil
		}
		seen[real] = true
		roots = append(roots, real)
		return nil
	}
	if err := add(dataDir); err != nil {
		return nil, err
	}
	if err := add(skillDir); err != nil {
		return nil, err
	}
	for _, p := range strings.FieldsFunc(extra, func(r rune) bool {
		return r == ',' || r == os.PathListSeparator || r == ';'
	}) {
		if err := add(p); err != nil {
			return nil, err
		}
	}
	return roots, nil
}

// AgentIsolationEnabled reports whether BLADEPRO_AGENT_USER was accepted and the
// execution surface is actually restricted. Used by SRC task creation when the
// operator requires sandboxed runs.
func AgentIsolationEnabled() bool { return agentIso.enabled }
