package config

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"
)

// View 是一个进程读配置的范围。列出的每一项是配置键（runner.url）或一段（sandbox），覆盖它自身
// 与其下的全部键。
//
// 给「可能与主程序不同版本的进程」用：多镜像部署的沙盒镜像由部署方本地构建，升级主程序后常常
// 还是旧的，而配置文件是随主程序更新的。这样的进程只要求自己读的键齐全，别的段加了键、删了键、
// 取值变了都不管。同一版本的进程（api、runner、serve……）仍整份加载：配置文件与程序不一致时
// 立刻发现。
type View []string

// ProcessView 返回一个进程读配置的视角：启动流程读的键（数据目录、日志），加上它自己另外读的。
// 读配置的进程一律经它构造，启动流程读的键不会漏。
func ProcessView(reads ...string) View {
	return append(View{"data_dir", "log"}, reads...)
}

// LoadView 按视角加载配置文件：视角覆盖的键必须齐全、取值有效，其余键不读、不查，返回的配置里
// 对应的字段是零值——调用方不得读它们（读了的键要登记进视角）。环境变量覆盖、由运行环境决定的
// 取值与 Load 相同。
func LoadView(path string, view View) (AppConfig, error) {
	keys, err := view.keys()
	if err != nil {
		return AppConfig{}, err
	}
	v, err := read(path)
	if err != nil {
		return AppConfig{}, err
	}
	if missing := missingKeys(v, keys); len(missing) > 0 {
		return AppConfig{}, missingError(path, missing)
	}
	scoped, err := restrict(v, keys)
	if err != nil {
		return AppConfig{}, err
	}
	return finish(scoped, path, view)
}

// keys 返回视角覆盖的全部叶子键（去重、排序）。视角里写了配置结构不认识的路径即报错：那是声明
// 写错了，照它加载会让本该校验的键静默漏掉。
func (v View) keys() ([]string, error) {
	var keys []string
	for _, p := range v {
		covered := false
		for _, key := range configKeys(reflect.TypeFor[AppConfig](), "") {
			if under(key, p) {
				covered = true
				keys = append(keys, key)
			}
		}
		if !covered {
			return nil, fmt.Errorf("配置视角里的 %q 不是配置键", p)
		}
	}
	slices.Sort(keys)
	return slices.Compact(keys), nil
}

// covers 报告视角是否覆盖配置键 key。
func (v View) covers(key string) bool {
	return slices.ContainsFunc(v, func(p string) bool { return under(key, p) })
}

// under 报告配置键 key 是否落在路径 p 之下：p 是它自身，或它所在的段。
func under(key, p string) bool {
	return key == p || strings.HasPrefix(key, p+".")
}

// restrict 返回只含 keys 的新配置源：文件里的其余键不进入解析，它们多了、类型变了都与读这个视角
// 的进程无关。
func restrict(v *viper.Viper, keys []string) (*viper.Viper, error) {
	settings := v.AllSettings() // 纯文件内容：此时尚未开启环境变量覆盖
	subset := make(map[string]any)
	for _, key := range keys {
		segments := strings.Split(key, ".")
		value, ok := lookup(settings, segments)
		if !ok {
			// 缺键已在此之前报出，取不到说明文件的结构不是映射（如某一段写了标量）。
			return nil, fmt.Errorf("读取配置文件里的键 %s 失败", key)
		}
		place(subset, segments, value)
	}
	raw, err := yaml.Marshal(subset)
	if err != nil {
		return nil, fmt.Errorf("整理配置键: %w", err)
	}
	out := viper.New()
	out.SetConfigType("yaml")
	if err := out.ReadConfig(bytes.NewReader(raw)); err != nil {
		return nil, fmt.Errorf("整理配置键: %w", err)
	}
	return out, nil
}

// lookup 按路径从嵌套的配置里取值；中间某一段不是映射即视为没有。
func lookup(settings map[string]any, segments []string) (any, bool) {
	for _, s := range segments[:len(segments)-1] {
		child, ok := settings[s].(map[string]any)
		if !ok {
			return nil, false
		}
		settings = child
	}
	value, ok := settings[segments[len(segments)-1]]
	return value, ok
}

// place 把 value 放到 nested 的 path 上，沿途建出各层的映射。
func place(nested map[string]any, path []string, value any) {
	for _, s := range path[:len(path)-1] {
		child, ok := nested[s].(map[string]any)
		if !ok {
			child = make(map[string]any)
			nested[s] = child
		}
		nested = child
	}
	nested[path[len(path)-1]] = value
}
