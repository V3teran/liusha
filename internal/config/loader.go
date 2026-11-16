package config

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/spf13/viper"
)

// Load 加载配置文件：文件必须存在且列出全部键（缺键、未知键都报错），再叠加 LIUSHA_ 环境变量，
// 解析由运行环境决定的取值（见 resolve）并校验取值。不校验密钥是否就位：各组件只在用到时检查。
func Load(path string) (AppConfig, error) {
	if path == "" {
		return AppConfig{}, errors.New("未指定配置文件")
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return AppConfig{}, fmt.Errorf("读取配置文件 %s: %w", path, err)
	}
	if missing := missingKeys(v); len(missing) > 0 {
		return AppConfig{}, fmt.Errorf("配置文件 %s 缺少键：%s（按同版本的 config.yaml 补齐）", path, strings.Join(missing, "、"))
	}
	// 文件已列出全部键：AutomaticEnv 只覆盖已存在的键，因此每个键都能被环境变量覆盖。
	// 值为空串的环境变量不覆盖（viper 缺省不认空值）。
	v.SetEnvPrefix("LIUSHA")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	var c AppConfig
	// UnmarshalExact：不认识的键（拼错、已删除的旧键）报错，不静默忽略。
	if err := v.UnmarshalExact(&c); err != nil {
		return AppConfig{}, fmt.Errorf("解析配置文件 %s: %w", path, err)
	}
	c, err := c.resolve()
	if err != nil {
		return AppConfig{}, err
	}
	if err := validate(c); err != nil {
		return AppConfig{}, err
	}
	return c, nil
}

// missingKeys 返回配置结构要求、但文件里没有的键（按 mapstructure 标签推导，排序后返回）。
func missingKeys(v *viper.Viper) []string {
	var missing []string
	for _, key := range configKeys(reflect.TypeFor[AppConfig](), "") {
		if !v.InConfig(key) {
			missing = append(missing, key)
		}
	}
	slices.Sort(missing)
	return missing
}

// configKeys 按 mapstructure 标签列出结构体的全部叶子键（嵌套结构体展开为 a.b）。
func configKeys(t reflect.Type, prefix string) []string {
	var keys []string
	for i := range t.NumField() {
		f := t.Field(i)
		tag, _, _ := strings.Cut(f.Tag.Get("mapstructure"), ",")
		if tag == "" || tag == "-" {
			continue
		}
		key := prefix + tag
		if f.Type.Kind() == reflect.Struct {
			keys = append(keys, configKeys(f.Type, key+".")...)
			continue
		}
		keys = append(keys, key)
	}
	return keys
}
