package mlog

// LogSafetyMode 日志安全模式
type LogSafetyMode int

const (
	// SafetyModeDefault 默认模式：异步日志使用安全格式化，同步日志直接格式化
	SafetyModeDefault LogSafetyMode = iota
	// SafetyModeAlways 始终使用安全格式化（性能较低，但最安全）
	SafetyModeAlways
	// SafetyModeNever 从不使用安全格式化（性能最高，但需要用户保证并发安全）
	SafetyModeNever
)

var (
	// 全局安全模式设置
	globalSafetyMode = SafetyModeDefault
)

// SetLogSafetyMode 设置日志安全模式
func SetLogSafetyMode(mode LogSafetyMode) {
	globalSafetyMode = mode
}

// GetLogSafetyMode 获取当前的日志安全模式
func GetLogSafetyMode() LogSafetyMode {
	return globalSafetyMode
}

// shouldUseSafeFormat 判断是否应该使用安全格式化
func shouldUseSafeFormat(isAsync bool) bool {
	switch globalSafetyMode {
	case SafetyModeAlways:
		return true
	case SafetyModeNever:
		return false
	case SafetyModeDefault:
		// 默认模式下，异步日志使用安全格式化
		return isAsync
	default:
		return isAsync
	}
}
