package executor

// MonitorConfig 是自我监察的配置。
type MonitorConfig struct {
	// Enabled 是否启用监察。
	Enabled bool

	// StepInterval 每 N 步评估一次。
	StepInterval int

	// EvaluateSteps 评估最近 N 步。
	EvaluateSteps int

	// SeverityThresholdForKill 触发 Kill 的严重度阈值。
	SeverityThresholdForKill string // "low" | "medium" | "high"
}

// DefaultMonitorConfig 返回默认监察配置。
func DefaultMonitorConfig() MonitorConfig {
	return MonitorConfig{
		Enabled:                  true,
		StepInterval:             5,
		EvaluateSteps:            5,
		SeverityThresholdForKill: "high",
	}
}

// WithMonitorConfig 使用配置结构配置监察。
func (a *Agent) WithMonitorConfig(config MonitorConfig) *Agent {
	a.monitorEnabled = config.Enabled
	a.monitorStepInterval = config.StepInterval
	a.monitorEvaluateSteps = config.EvaluateSteps
	return a
}
