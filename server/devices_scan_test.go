package server

import "testing"

func TestParseADBDevices(t *testing.T) {
	out := "List of devices attached\r\n" +
		"emulator-5554          device product:sdk_gphone64_x86_64 model:sdk_gphone64_x86_64 device:emu64xa transport_id:1\r\n" +
		"R58M12345              unauthorized usb:1-1 transport_id:2\r\n" +
		"\r\n"
	list := parseADBDevices(out)
	if len(list) != 2 {
		t.Fatalf("期望 2 台设备，得到 %d：%+v", len(list), list)
	}
	emu := list[0]
	if emu.Serial != "emulator-5554" || emu.State != "device" {
		t.Errorf("序列号/状态解析错误：%+v", emu)
	}
	if emu.Model != "sdk_gphone64_x86_64" || emu.Product != "sdk_gphone64_x86_64" || emu.Device != "emu64xa" {
		t.Errorf("型号字段解析错误（下划线应保留）：%+v", emu)
	}
	if emu.TransportID != "1" {
		t.Errorf("transport_id 解析错误：%+v", emu)
	}
	if list[1].State != "unauthorized" {
		t.Errorf("unauthorized 状态解析错误：%+v", list[1])
	}
}

func TestParseADBDevicesEmpty(t *testing.T) {
	if got := parseADBDevices("List of devices attached\n"); len(got) != 0 {
		t.Errorf("空列表应返回 0 台，得到 %+v", got)
	}
}
