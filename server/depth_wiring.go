package server

import (
	"github.com/lin9527ya/bladepro/agent"
)

// P-Q 深度分级与厂商档案的宿主接线。
//
// 两个 provider 都受 settings 开关控制（默认关，灰度）：
//   - depth_enabled：打开后 planner/worker 注入 L1/L2/L3 深度契约；
//   - vendor_block_enabled：打开后注入公司档案（归属域名/窗口/不收洞型/定级口径）。
//
// 深度解析优先级（db/company_profiles.go EffectiveDepth）：
// tasks.depth_override > mode_doc.max_depth（program 编译） > MAX(companies.profile_max_depth) > 2。
func (s *Server) wireDepthProviders() {
	agent.DepthProvider = func(taskID int64) int {
		if s == nil || s.m == nil || s.m.pg == nil || taskID <= 0 {
			return -1
		}
		if v, _, _ := s.m.pg.GetSetting(settingDepthEnabled); v != "true" {
			return -1
		}
		return s.m.PG().Companies().EffectiveDepth(taskID)
	}
	agent.VendorProvider = func(taskID int64) string {
		if s == nil || s.m == nil || s.m.pg == nil || taskID <= 0 {
			return ""
		}
		if v, _, _ := s.m.pg.GetSetting(settingVendorBlockEnabled); v != "true" {
			return ""
		}
		return s.m.PG().Companies().VendorContextBlock(taskID)
	}
}
