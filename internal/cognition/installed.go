package cognition

import (
	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/tools"
)

// restrictToInstalled 把 agent 的工具名单收窄为「允许 ∩ 已安装」：「全部」（*）先按工具全集展开，
// 再剔除沙盒未安装的 CLI 工具与浏览器不可用时的 drive_browser。名单为 nil（拒绝）保持 nil。
// 被剔除的工具记日志，便于排查「已允许却没用上」。
func (d *Deps) restrictToInstalled(cfg agent.Agent) agent.Agent {
	var fnAll, cliAll []string
	for _, meta := range registry.FunctionToolCatalog {
		fnAll = append(fnAll, meta.Name)
	}
	if d.ToolsManifest != nil {
		cliAll = d.ToolsManifest.Names()
	}
	var dropped []string
	keep := func(allowed, all []string, installed func(string) bool) []string {
		if allowed == nil {
			return nil
		}
		out := []string{}
		for _, name := range all {
			if !registry.Allows(allowed, name) {
				continue
			}
			if !installed(name) {
				dropped = append(dropped, name)
				continue
			}
			out = append(out, name)
		}
		return out
	}
	inv := d.Installed
	cfg.FunctionTools = keep(cfg.FunctionTools, fnAll, func(n string) bool { return tools.FunctionInstalled(inv, n) })
	cfg.CliTools = keep(cfg.CliTools, cliAll, func(n string) bool { return tools.CLIInstalled(inv, n) })
	if len(dropped) > 0 {
		d.Logger.Info().Str("agent", cfg.Code).Strs("tools", dropped).Msg("沙盒未安装，本轮不可用的已允许工具")
	}
	return cfg
}
