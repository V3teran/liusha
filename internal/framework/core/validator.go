package core

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// Validator 是类型验证器。
type Validator struct {
	schemas map[string]json.RawMessage // type name -> JSON Schema
}

// NewValidator 创建验证器。
func NewValidator() *Validator {
	return &Validator{
		schemas: make(map[string]json.RawMessage),
	}
}

// RegisterSchema 注册类型的 JSON Schema。
func (v *Validator) RegisterSchema(typeName string, schema json.RawMessage) {
	v.schemas[typeName] = schema
}

// Validate 验证值是否符合类型。
func (v *Validator) Validate(typeName string, value any) error {
	schema, exists := v.schemas[typeName]
	if !exists {
		return fmt.Errorf("schema not found for type: %s", typeName)
	}

	// 简化实现：仅做基本类型检查
	// 实际应使用 JSON Schema 验证库（如 gojsonschema）
	_ = schema

	return v.validateBasicType(value)
}

// validateBasicType 验证基本类型。
func (v *Validator) validateBasicType(value any) error {
	if value == nil {
		return fmt.Errorf("value is nil")
	}

	// 检查是否可序列化
	_, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("value is not serializable: %w", err)
	}

	return nil
}

// ValidateStruct 验证结构体字段。
func (v *Validator) ValidateStruct(value any) error {
	rv := reflect.ValueOf(value)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("expected struct, got %s", rv.Kind())
	}

	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		fieldValue := rv.Field(i)

		// 检查必填字段
		if tag := field.Tag.Get("validate"); tag == "required" {
			if fieldValue.IsZero() {
				return fmt.Errorf("required field %s is zero", field.Name)
			}
		}
	}

	return nil
}

// ============================================
// 类型转换器
// ============================================

// TypeConverter 类型转换器。
type TypeConverter struct{}

// NewTypeConverter 创建类型转换器。
func NewTypeConverter() *TypeConverter {
	return &TypeConverter{}
}

// Convert 转换类型。
func (c *TypeConverter) Convert(value any, targetType reflect.Type) (any, error) {
	rv := reflect.ValueOf(value)

	if rv.Type().ConvertibleTo(targetType) {
		return rv.Convert(targetType).Interface(), nil
	}

	return nil, fmt.Errorf("cannot convert %s to %s", rv.Type(), targetType)
}

// ConvertToString 转换为字符串。
func (c *TypeConverter) ConvertToString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	case fmt.Stringer:
		return v.String(), nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

// ConvertToInt 转换为整数。
func (c *TypeConverter) ConvertToInt(value any) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	case string:
		var i int
		_, err := fmt.Sscanf(v, "%d", &i)
		return i, err
	default:
		return 0, fmt.Errorf("cannot convert %T to int", value)
	}
}

// ConvertToFloat 转换为浮点数。
func (c *TypeConverter) ConvertToFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case string:
		var f float64
		_, err := fmt.Sscanf(v, "%f", &f)
		return f, err
	default:
		return 0, fmt.Errorf("cannot convert %T to float64", value)
	}
}

// ============================================
// 类型检查器
// ============================================

// TypeChecker 类型检查器。
type TypeChecker struct{}

// NewTypeChecker 创建类型检查器。
func NewTypeChecker() *TypeChecker {
	return &TypeChecker{}
}

// IsNil 检查是否为 nil。
func (c *TypeChecker) IsNil(value any) bool {
	if value == nil {
		return true
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return rv.IsNil()
	default:
		return false
	}
}

// IsZero 检查是否为零值。
func (c *TypeChecker) IsZero(value any) bool {
	if value == nil {
		return true
	}

	rv := reflect.ValueOf(value)
	return rv.IsZero()
}

// IsSameType 检查两个值是否同类型。
func (c *TypeChecker) IsSameType(a, b any) bool {
	return reflect.TypeOf(a) == reflect.TypeOf(b)
}

// IsAssignable 检查 a 是否可赋值给 b 的类型。
func (c *TypeChecker) IsAssignable(a, b any) bool {
	ta := reflect.TypeOf(a)
	tb := reflect.TypeOf(b)
	return ta.AssignableTo(tb)
}

// GetTypeName 获取类型名称。
func (c *TypeChecker) GetTypeName(value any) string {
	if value == nil {
		return "nil"
	}
	return reflect.TypeOf(value).String()
}
