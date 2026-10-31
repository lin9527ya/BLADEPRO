package agent

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/lin9527ya/Norma/llm"
	actool "github.com/lin9527ya/Norma/tool"
)

type failureHintTool struct{ actool.CoreTool }

func (t *failureHintTool) Call(ctx context.Context, in json.RawMessage, tc *actool.ToolContext) (actool.Result, error) {
	res, err := t.CoreTool.Call(ctx, in, tc)
	if err == nil && res.IsError {
		if hint := failureHint(res.Flatten()); hint != "" {
			res.Content = append(res.Content, llm.TextBlock("\n"+hint))
		}
	}
	return res, err
}

// WithFailureHint appends one actionable next step to failed command output.
// Only IsError results are touched: a successful scan may legitimately contain
// strings like "permission denied" as target content.
func WithFailureHint(t actool.CoreTool) actool.CoreTool { return &failureHintTool{CoreTool: t} }

func failureHint(text string) string {
	low := strings.ToLower(text)
	switch {
	case strings.Contains(text, "[exit code 127]") || strings.Contains(low, "command not found"):
		return "提示：命令不存在。换用已安装的同类工具，或先 which <cmd> 确认路径。"
	case strings.Contains(low, "permission denied"):
		return "提示：权限不足。换一条不需要该权限的路径，或先确认当前身份与可写位置。"
	case strings.Contains(low, "connection refused"):
		return "提示：连接被拒。先确认端口是否开放、服务是否在该地址监听，再重试。"
	case strings.Contains(low, "no such file or directory"):
		return "提示：路径不存在。先列出父目录确认实际位置，别猜路径。"
	case strings.Contains(low, "timed out") || strings.Contains(low, "timeout"):
		return "提示：执行超时。长任务改用 run_execution 后台跑，再用 get_execution 取输出。"
	case strings.Contains(text, "[exit code "):
		return "提示：命令非零退出。先读上面的报错定位原因，别原样重试。"
	}
	return ""
}
