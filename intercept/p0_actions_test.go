package intercept

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestOpaqueMCPAndConfigurationActionsRespectEmergencyStop(t *testing.T) {
	d := killSwitchDB(t)
	defer setSetting(t, d, SettingKillSwitch, "")()
	if err := WriteKillSwitch(d, KillSwitchReceipt{On: true, Reason: "P0 fixture", At: time.Now().Format(time.RFC3339)}); err != nil {
		t.Fatal(err)
	}
	i := New(d)
	for _, name := range []string{"mcp__opaque__invoke", "create_mcp", "update_custom_tool", "Write"} {
		decision, hit := i.CheckScope(context.Background(), 0, name, json.RawMessage(`{}`))
		if !hit || decision.Action != "deny" {
			t.Fatalf("%s bypassed stop: %+v", name, decision)
		}
	}
	if _, hit := i.CheckScope(context.Background(), 0, "record_fact", json.RawMessage(`{}`)); hit {
		t.Fatal("stop prevented internal fact settlement")
	}
}
