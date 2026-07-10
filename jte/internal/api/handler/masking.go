package handler

// ============================================================================
// 等保2.0 敏感数据脱敏
//
// 在响应返回前对手机号/身份证/车牌/邮箱/姓名等敏感字段进行脱敏处理，
// 避免明文泄露给前端。脱敏在 handler 层完成，存储层仍保留明文（或国密密文）。
//
// 用法：
//   MaskVehicle(vehicle) — 脱敏车辆信息（手机号、车牌号）
//   MaskVehicles(items)  — 批量脱敏
//   MaskMap(m)           — 通用 map 脱敏（按字段名自动识别）
// ============================================================================

import (
	"reflect"

	"github.com/jte-engine/jte/pkg/masking"
	"github.com/jte-engine/jte/pkg/storage"
)

// maskString 对敏感字段按类型脱敏，返回脱敏后的字符串
func maskString(field, val string) string {
	if val == "" {
		return val
	}
	if ok, t := masking.IsSensitiveField(field); ok {
		return masking.MaskByType(t, val)
	}
	return val
}

// MaskMap 对 map[string]interface{} 中的敏感字段脱敏（原地修改）
// 递归处理嵌套 map；按字段名自动识别敏感字段类型。
func MaskMap(m map[string]interface{}) {
	if m == nil {
		return
	}
	for k, v := range m {
		if s, ok := v.(string); ok {
			m[k] = maskString(k, s)
			continue
		}
		if sub, ok := v.(map[string]interface{}); ok {
			MaskMap(sub)
		}
	}
}

// maskStruct 通过反射脱敏结构体的 string 字段（按 json tag 识别字段名）
// 返回新 map，避免修改原结构体
func maskStruct(v interface{}) map[string]interface{} {
	if v == nil {
		return nil
	}
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr {
		if rv.IsNil() {
			return nil
		}
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return nil
	}
	result := make(map[string]interface{})
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		name := jsonTag
		if idx := indexOfByte(name, ','); idx >= 0 {
			name = name[:idx]
		}
		fv := rv.Field(i)
		if fv.Kind() == reflect.String {
			result[name] = maskString(name, fv.String())
			continue
		}
		result[name] = fv.Interface()
	}
	return result
}

func indexOfByte(s string, b byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

// MaskVehicle 脱敏车辆信息（手机号 + 车牌号）
// 返回脱敏后的 map，原结构体不被修改。
func MaskVehicle(v *storage.Vehicle) map[string]interface{} {
	if v == nil {
		return nil
	}
	m := maskStruct(v)
	// 兜底：确保 Phone/PlateNo 一定被脱敏（即便 IsSensitiveField 未匹配）
	if orig, ok := m["phone"].(string); ok && orig != "" {
		m["phone"] = masking.MaskPhone(orig)
	}
	if orig, ok := m["plate_no"].(string); ok && orig != "" {
		m["plate_no"] = masking.MaskPlate(orig)
	}
	return m
}

// MaskVehicles 批量脱敏车辆信息
func MaskVehicles(items []*storage.Vehicle) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(items))
	for _, v := range items {
		result = append(result, MaskVehicle(v))
	}
	return result
}
