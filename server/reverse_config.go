package server

import (
	"strings"

	"github.com/lin9527ya/bladepro/agent"
)

// P-Q 逆向/看图/飞书工具的配置装配（移植自 quiver ReverseConfig/VisionConfig）。
//
// 设置项（/api/settings）：
//
//	jadx_bin / jadx_out_dir / ghidra_home / dnslog_api
//	vision_base_url / vision_api_key / vision_model
//	feishu_app_id / feishu_app_secret / feishu_chat_id
//
// 密钥只写不读：payload 只回 *_set 布尔；put 时 null=不改、""=清空。
// 每次 settings 变更后重新装配（工具读包级变量，无需重建 agent）。
func (s *Server) applyReverseConfig() {
	get := func(key string) string {
		v, _, _ := s.m.pg.GetSetting(key)
		return strings.TrimSpace(v)
	}
	visionKey := get(settingVisionAPIKey)
	visionBase := get(settingVisionBaseURL)
	visionModel := get(settingVisionModel)
	// 没单独配 vision key 时跟随全局激活 LLM profile（只读库，不落新副本）。
	if p, err := s.m.pg.ActiveProfile(); err == nil && p != nil {
		if visionKey == "" {
			visionKey = p.APIKey
		}
		if visionBase == "" {
			visionBase = p.BaseURL
		}
		if visionModel == "" {
			visionModel = p.Model
		}
	}
	agent.ReverseDefaults = &agent.ReverseConfig{
		JadxBin:         get(settingJadxBin),
		JadxOutDir:      get(settingJadxOutDir),
		GhidraHome:      get(settingGhidraHome),
		DnslogAPI:       get(settingDnslogAPI),
		FeishuAppID:     get(settingFeishuAppID),
		FeishuAppSecret: get(settingFeishuAppSecret),
		FeishuChatID:    get(settingFeishuChatID),
	}
	if visionKey != "" {
		agent.VisionDefaults = &agent.VisionConfig{
			BaseURL: visionBase,
			APIKey:  visionKey,
			Model:   visionModel,
		}
	} else {
		agent.VisionDefaults = nil
	}
}
