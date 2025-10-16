package mlog

import (
	"fmt"
	"reflect"
	"sync"
)

// SafeFormatter 提供并发安全的格式化功能
type SafeFormatter struct {
	// 使用对象池减少内存分配
	bufPool sync.Pool
}

// NewSafeFormatter 创建新的安全格式化器
func NewSafeFormatter() *SafeFormatter {
	return &SafeFormatter{
		bufPool: sync.Pool{
			New: func() interface{} {
				buf := make([]byte, 0, 1024)
				return &buf
			},
		},
	}
}

// FormatSafely 安全地格式化参数，避免并发问题
func (sf *SafeFormatter) FormatSafely(format string, args ...interface{}) string {
	if len(args) == 0 {
		return format
	}

	// 将所有参数转换为安全的表示形式
	safeArgs := make([]interface{}, len(args))
	for i, arg := range args {
		safeArgs[i] = sf.makeArgSafe(arg)
	}

	// 使用安全的参数进行格式化
	return fmt.Sprintf(format, safeArgs...)
}

// makeArgSafe 将参数转换为并发安全的形式
func (sf *SafeFormatter) makeArgSafe(arg interface{}) interface{} {
	if arg == nil {
		return nil
	}

	// 对于基本类型，直接返回
	switch v := arg.(type) {
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, complex64, complex128,
		string:
		return v
	case []byte:
		// 字节切片需要复制
		copied := make([]byte, len(v))
		copy(copied, v)
		return copied
	case error:
		// 错误类型转换为字符串
		return v.Error()
	}

	// 对于复杂类型，使用反射处理
	return sf.makeComplexArgSafe(arg)
}

// makeComplexArgSafe 处理复杂类型的参数
func (sf *SafeFormatter) makeComplexArgSafe(arg interface{}) interface{} {
	val := reflect.ValueOf(arg)

	// 处理空指针
	if !val.IsValid() {
		return nil
	}

	switch val.Kind() {
	case reflect.Ptr:
		if val.IsNil() {
			return nil
		}
		// 解引用指针并递归处理
		return sf.makeArgSafe(val.Elem().Interface())

	case reflect.Map:
		// 对于 map，创建一个快照字符串表示
		// 这完全避免了并发访问的问题
		return sf.mapToSafeString(val)

	case reflect.Slice, reflect.Array:
		// 创建切片的副本
		return sf.sliceToSafe(val)

	case reflect.Struct:
		// 结构体转换为 map 表示
		return sf.structToSafeMap(val)

	case reflect.Interface:
		if val.IsNil() {
			return nil
		}
		// 递归处理接口的实际值
		return sf.makeArgSafe(val.Elem().Interface())

	case reflect.Chan, reflect.Func:
		// 通道和函数无法安全序列化，返回类型信息
		return fmt.Sprintf("<%s>", val.Type().String())

	default:
		// 其他类型使用默认的字符串表示
		return fmt.Sprintf("%v", arg)
	}
}

// mapToSafeString 将 map 转换为安全的字符串表示
// 优化：尝试获取 map 长度以提供更多信息
func (sf *SafeFormatter) mapToSafeString(val reflect.Value) string {
	if val.IsNil() {
		return "nil"
	}

	// 获取 map 的类型信息
	mapType := val.Type().String()

	// 策略：尝试获取 map 长度（带 panic 保护）
	// 在大多数情况下，获取长度是安全的，只有在极端并发冲突时才会 panic
	length := -1
	func() {
		defer func() {
			if recover() != nil {
				// 发生并发冲突，无法获取长度
				length = -1
			}
		}()
		length = val.Len()
	}()

	// 根据获取结果返回不同的表示
	if length >= 0 {
		// 成功获取长度
		if length == 0 {
			return fmt.Sprintf("%s{}", mapType)
		}
		return fmt.Sprintf("%s{len=%d}", mapType, length)
	}

	// 无法获取长度（并发冲突），标记为 concurrent
	return fmt.Sprintf("%s{concurrent}", mapType)
}

// sliceToSafe 将切片转换为安全的表示
func (sf *SafeFormatter) sliceToSafe(val reflect.Value) interface{} {
	// 数组不能调用 IsNil
	if val.Kind() == reflect.Slice && val.IsNil() {
		return nil
	}

	length := val.Len()

	// 对于小切片，创建副本
	if length <= 10 {
		result := make([]interface{}, length)
		for i := 0; i < length; i++ {
			result[i] = sf.makeArgSafe(val.Index(i).Interface())
		}
		return result
	}

	// 对于大切片，返回摘要信息
	return fmt.Sprintf("[%d items of %s]", length, val.Type().Elem().String())
}

// structToSafeMap 将结构体转换为安全的 map 表示
func (sf *SafeFormatter) structToSafeMap(val reflect.Value) interface{} {
	typ := val.Type()
	result := make(map[string]interface{})

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)

		// 只处理导出的字段
		if field.PkgPath != "" {
			continue
		}

		fieldVal := val.Field(i)

		// 跳过零值
		if isZeroValue(fieldVal) {
			continue
		}

		// 递归处理字段值
		result[field.Name] = sf.makeArgSafe(fieldVal.Interface())
	}

	return result
}

// isZeroValue 检查是否为零值
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	case reflect.Struct:
		// 结构体总是返回 false，让调用者决定
		return false
	default:
		return false
	}
}

// SafeFormat 全局安全格式化函数
var globalSafeFormatter = NewSafeFormatter()

// SafeFormat 安全地格式化日志消息
func SafeFormat(format string, args ...interface{}) string {
	return globalSafeFormatter.FormatSafely(format, args...)
}
