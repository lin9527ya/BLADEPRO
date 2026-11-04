package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	actool "github.com/lin9527ya/Norma/tool"

	"github.com/lin9527ya/bladepro/remoteshell"
)

// P-Q 设备工具（移植自 quiver devices）：在已登记的设备上执行只读命令。
// 动作类工具：受作业时间窗/拦截规则/审计约束（intercept.builtinActionTools）。
func (t *ToolSet) deviceTools() []actool.CoreTool {
	return []actool.CoreTool{t.deviceList(), t.deviceExec()}
}

func (t *ToolSet) deviceList() actool.CoreTool {
	return readTool("device_list",
		"列出已登记的测试设备（adb/ssh/local）与在线状态。执行命令前先看这里有哪些真机可用。",
		obj(map[string]any{}),
		func(_ context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("device_list 未启用: AssetStore 未初始化"), nil
			}
			list, err := t.as.ListDevices()
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			return jsonResult(map[string]any{"devices": list, "count": len(list)})
		})
}

func (t *ToolSet) deviceExec() actool.CoreTool {
	return writeTool("device_exec",
		"在已登记的设备上执行命令：kind=adb 走 `adb -s <conn> shell`；kind=ssh 需配置私钥（password 模式请手工登录）。\n"+
			"纪律：只读命令优先；不删改设备数据、不安装来源不明的应用；命令与输出会被审计。",
		obj(map[string]any{
			"device_id": idp("设备 id（device_list 返回）"),
			"command":   str("要在设备上执行的命令（adb shell / ssh 远程命令）"),
			"timeout_s": intp("超时秒数（默认 60，上限 300）"),
		}, "device_id", "command"),
		func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			if t.as == nil {
				return actool.Errorf("device_exec 未启用: AssetStore 未初始化"), nil
			}
			var a struct {
				DeviceID int64  `json:"device_id"`
				Command  string `json:"command"`
				TimeoutS int    `json:"timeout_s"`
			}
			if err := json.Unmarshal(in, &a); err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if a.DeviceID <= 0 || strings.TrimSpace(a.Command) == "" {
				return actool.Errorf("device_id 与 command 必填"), nil
			}
			d, pass, err := t.as.DeviceByID(a.DeviceID)
			if err != nil {
				return actool.Errorf(err.Error()), nil
			}
			if d == nil {
				return actool.Errorf(fmt.Sprintf("设备 %d 不存在", a.DeviceID)), nil
			}
			timeout := a.TimeoutS
			if timeout <= 0 {
				timeout = 60
			}
			if timeout > 300 {
				timeout = 300
			}
			ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel()
			var cmd *exec.Cmd
			switch d.Kind {
			case "adb":
				serial := strings.TrimSpace(d.Conn)
				if serial == "" {
					return actool.Errorf("adb 设备缺少 conn（序列号）"), nil
				}
				cmd = exec.CommandContext(ctx, "adb", "-s", serial, "shell", a.Command)
			case "ssh":
				if strings.TrimSpace(d.SSHHost) == "" {
					return actool.Errorf("ssh 设备缺少 ssh_host"), nil
				}
				out, err := remoteshell.Run(ctx, remoteshell.Target{
					Host: d.SSHHost, Port: d.SSHPort, User: d.SSHUser,
					Password: pass, KeyPath: d.SSHKey,
				}, a.Command)
				if len(out) > 32<<10 {
					out = out[:32<<10] + "\n…（输出截断）"
				}
				if err != nil {
					return actool.Text(fmt.Sprintf("命令失败：%v\n%s", err, out)), nil
				}
				return actool.Text(out), nil
			default:
				return actool.Errorf("kind=local 请直接用 Bash 执行（device_exec 只覆盖 adb/ssh）"), nil
			}
			out, err := cmd.CombinedOutput()
			text := string(out)
			if len(text) > 32<<10 {
				text = text[:32<<10] + "\n…（输出截断）"
			}
			if err != nil {
				return actool.Text(fmt.Sprintf("命令失败：%v\n%s", err, text)), nil
			}
			return actool.Text(text), nil
		})
}
