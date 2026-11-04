package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/lin9527ya/bladepro/db"
	"github.com/lin9527ya/bladepro/remoteshell"
)

// P-Q 设备注册表 API（移植自 quiver devices_api）：
// adb/ssh/local 三类设备的 CRUD 与探活；凭据不回显（ssh_pass 只写）。
func (s *Server) registerDeviceRoutes(mux routeRegistrar) {
	mux.HandleFunc("GET /api/devices", s.listDevices)
	mux.HandleFunc("POST /api/devices", s.upsertDevice)
	mux.HandleFunc("DELETE /api/devices/{id}", s.deleteDevice)
	mux.HandleFunc("POST /api/devices/{id}/probe", s.probeDevice)
	// SSH 连通性测试：凭据直传、不落库，用于登记前确认能登录。
	mux.HandleFunc("POST /api/devices/ssh-test", s.testSSHDevice)
	// P-Q：ADB 扫描发现（adbd 已连接的真机一键登记）。
	mux.HandleFunc("GET /api/devices/scan/adb", s.scanADBDevices)
	mux.HandleFunc("POST /api/devices/scan/adb/import", s.importADBDevices)
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.knowledgeStore().ListDevices()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"devices": list, "count": len(list)})
}

func (s *Server) upsertDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		db.Device
		SSHPass string `json:"ssh_pass"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	out, err := s.knowledgeStore().UpsertDevice(req.Device, req.SSHPass)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, out)
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	if err := s.knowledgeStore().DeleteDevice(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

// probeDevice 探活：adb 实际执行一条无害命令；ssh 有凭据时走原生登录测试，
// 没有凭据时退化为 TCP 可达性；local 恒在线。
func (s *Server) probeDevice(w http.ResponseWriter, r *http.Request) {
	id := parseIntDefault(r.PathValue("id"))
	if id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	d, pass, err := s.knowledgeStore().DeviceByID(id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if d == nil {
		writeErr(w, 404, "设备不存在")
		return
	}
	online := false
	note := ""
	switch d.Kind {
	case "local":
		online, note = true, "local：本机"
	case "adb":
		if _, err := exec.LookPath("adb"); err != nil {
			note = "未找到 adb（请安装 platform-tools 并加入 PATH）"
			break
		}
		serial := strings.TrimSpace(d.Conn)
		if serial == "" {
			out, err := exec.Command("adb", "devices").CombinedOutput()
			if err != nil {
				note = "adb devices 失败：" + err.Error()
				break
			}
			online = strings.Contains(string(out), "\tdevice")
			if online {
				note = "adb 可用，有在线设备"
			} else {
				note = "adb 可用，但没有在线设备"
			}
			break
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "adb", "-s", serial, "shell", "echo", "ok").CombinedOutput()
		text := strings.TrimSpace(string(out))
		online = err == nil && strings.Contains(text, "ok")
		if online {
			note = "adb shell 在线"
		} else if err != nil {
			note = "adb shell 失败：" + err.Error() + "（" + clipNote(text) + "）"
		} else {
			note = "adb shell 输出异常：" + clipNote(text)
		}
	case "ssh":
		if d.SSHHost == "" {
			note = "SSH 设备缺少 ssh_host"
			break
		}
		if pass == "" && strings.TrimSpace(d.SSHKey) == "" {
			addr := net.JoinHostPort(d.SSHHost, fmt.Sprintf("%d", d.SSHPort))
			conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
			if err != nil {
				note = "TCP 连接失败：" + err.Error()
				break
			}
			_ = conn.Close()
			online, note = true, "TCP 可达（未配置密码/私钥，未验证登录）"
			break
		}
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := remoteshell.Test(ctx, sshTarget(*d, pass)); err != nil {
			note = "SSH 登录失败：" + err.Error()
			break
		}
		online, note = true, "SSH 登录成功，命令可执行"
	default:
		note = "未知设备类型"
	}
	_ = s.knowledgeStore().RecordDeviceProbe(id, online, note)
	writeJSON(w, 200, map[string]any{"ok": online, "note": note})
}

func sshTarget(d db.Device, pass string) remoteshell.Target {
	return remoteshell.Target{
		Host: d.SSHHost, Port: d.SSHPort, User: d.SSHUser,
		Password: pass, KeyPath: d.SSHKey,
	}
}

func clipNote(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "无输出"
	}
	if len(s) > 160 {
		return s[:160] + "…"
	}
	return s
}

// testSSHDevice 用请求里直传的凭据测试 SSH 登录（凭据不落库、不记录）。
func (s *Server) testSSHDevice(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host    string `json:"host"`
		Port    int    `json:"port"`
		User    string `json:"user"`
		Pass    string `json:"pass"`
		KeyPath string `json:"key_path"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if strings.TrimSpace(req.Host) == "" || strings.TrimSpace(req.User) == "" {
		writeErr(w, 400, "主机和用户名必填")
		return
	}
	if req.Port <= 0 {
		req.Port = 22
	}
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	t := remoteshell.Target{
		Host: strings.TrimSpace(req.Host), Port: req.Port, User: strings.TrimSpace(req.User),
		Password: req.Pass, KeyPath: req.KeyPath,
	}
	if err := remoteshell.Test(ctx, t); err != nil {
		writeJSON(w, 200, map[string]any{"online": false, "note": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"online": true, "note": "SSH 连接成功，命令可执行"})
}

// ADBDevice 是 `adb devices -l` 解析出的一台设备。
type ADBDevice struct {
	Serial      string `json:"serial"`
	State       string `json:"state"`
	Model       string `json:"model,omitempty"`
	Product     string `json:"product,omitempty"`
	Device      string `json:"device,omitempty"`
	TransportID string `json:"transport_id,omitempty"`
	Registered  bool   `json:"registered"`
}

// parseADBDevices 解析 `adb devices -l` 输出（跳过表头与空行）。
func parseADBDevices(out string) []ADBDevice {
	list := []ADBDevice{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		d := ADBDevice{Serial: fields[0], State: fields[1]}
		for _, f := range fields[2:] {
			k, v, ok := strings.Cut(f, ":")
			if !ok {
				continue
			}
			switch k {
			case "model":
				d.Model = v
			case "product":
				d.Product = v
			case "device":
				d.Device = v
			case "transport_id":
				d.TransportID = v
			}
		}
		list = append(list, d)
	}
	return list
}

// scanADBDevices 扫描已连接（adbd 可见）的设备，并标出哪些已登记。
func (s *Server) scanADBDevices(w http.ResponseWriter, r *http.Request) {
	if _, err := exec.LookPath("adb"); err != nil {
		writeErr(w, 400, "未找到 adb：请安装 Android platform-tools 并加入 PATH，或用手动登记")
		return
	}
	out, err := exec.Command("adb", "devices", "-l").CombinedOutput()
	if err != nil {
		writeErr(w, 500, "adb devices 失败："+err.Error()+"（"+strings.TrimSpace(string(out))+"）")
		return
	}
	devices := parseADBDevices(string(out))
	registered := map[string]bool{}
	if rows, err := s.knowledgeStore().ListDevices(); err == nil {
		for _, d := range rows {
			if d.Kind == "adb" {
				registered[strings.TrimSpace(d.Conn)] = true
			}
		}
	}
	for i := range devices {
		devices[i].Registered = registered[devices[i].Serial]
	}
	writeJSON(w, 200, map[string]any{"devices": devices, "count": len(devices), "raw": strings.TrimSpace(string(out))})
}

// importADBDevices 把扫描结果里选中的序列号登记为设备（已存在则跳过）。
func (s *Server) importADBDevices(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Serials []string `json:"serials"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad json: "+err.Error())
		return
	}
	if len(req.Serials) == 0 {
		writeErr(w, 400, "serials 不能为空")
		return
	}
	// 重新扫描一次，拿到 model 作为默认名称（避免信任客户端传来的名称）。
	out, err := exec.Command("adb", "devices", "-l").CombinedOutput()
	if err != nil {
		writeErr(w, 500, "adb devices 失败："+err.Error())
		return
	}
	found := map[string]ADBDevice{}
	for _, d := range parseADBDevices(string(out)) {
		found[d.Serial] = d
	}
	existing := map[string]bool{}
	if rows, err := s.knowledgeStore().ListDevices(); err == nil {
		for _, d := range rows {
			if d.Kind == "adb" {
				existing[strings.TrimSpace(d.Conn)] = true
			}
		}
	}
	created, skipped, errs := []string{}, []string{}, []string{}
	for _, serial := range req.Serials {
		serial = strings.TrimSpace(serial)
		if serial == "" {
			continue
		}
		if existing[serial] {
			skipped = append(skipped, serial)
			continue
		}
		d, ok := found[serial]
		if !ok {
			errs = append(errs, serial+": 当前不在 adb devices 列表里")
			continue
		}
		name := strings.TrimSpace(d.Model)
		if name == "" {
			name = serial
		}
		if _, err := s.knowledgeStore().UpsertDevice(db.Device{
			Name: name, Kind: "adb", Conn: serial, ToolsMD: strings.TrimSpace(d.Product),
		}, ""); err != nil {
			errs = append(errs, serial+": "+err.Error())
			continue
		}
		created = append(created, serial)
	}
	writeJSON(w, 200, map[string]any{"created": created, "skipped": skipped, "errors": errs})
}
