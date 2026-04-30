package ingestor

// SiteIngest v1.5 整站模式入口（mode=site，HTTP API /scan/site 触发）。
//
// 流程：HTTP API /scan/site 接收 start_url → 创建 engagement (mode=browser)
// → tasks.Create(role=main) + payload{mode:"site", entrypoint:{start_url}}
// → Asynq react queue → scanner 主 ReAct (handleSite)
//
// v1 不实现，仅占位声明扩展点。
func SiteIngest() {
	// TODO(v1.5): 实现整站模式入口
}
