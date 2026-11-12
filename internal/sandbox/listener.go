package sandbox

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseListenerPorts 解析对外监听段配置（"起-止"，含两端）。空串 = 不提供（0, 0, nil）。
// 段约束：端口 1-65535、起 ≤ 止、宽度 ≤ 1000（有界段是整个机制的前提——无界等于把
// 宿主端口面全交给沙盒内进程）。
func ParseListenerPorts(spec string) (start, count int, err error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, nil
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("监听段格式应为 \"起-止\"，实得 %q", spec)
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil {
		return 0, 0, fmt.Errorf("监听段端口须为整数: %q", spec)
	}
	if lo < 1 || hi < 1 || lo > 65535 || hi > 65535 {
		return 0, 0, fmt.Errorf("监听段端口须在 1-65535: %q", spec)
	}
	if hi < lo {
		return 0, 0, fmt.Errorf("监听段起点大于终点: %q", spec)
	}
	if hi-lo+1 > 1000 {
		return 0, 0, fmt.Errorf("监听段过宽（%d 个，上限 1000）: %q", hi-lo+1, spec)
	}
	return lo, hi - lo + 1, nil
}
