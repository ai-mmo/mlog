package mlog

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// levelCacheMutex 保护 levelCache 的并发访问
var levelCacheMutex sync.RWMutex

// levelCache 级别缓存映射，用于快速查找日志级别。
// 避免重复解析字符串级别，提高性能。
// 注意：访问时需要使用 levelCacheMutex 保护
var levelCache = map[string]zapcore.Level{
	"debug":  zapcore.DebugLevel,
	"info":   zapcore.InfoLevel,
	"warn":   zapcore.WarnLevel,
	"error":  zapcore.ErrorLevel,
	"dpanic": zapcore.DPanicLevel,
	"panic":  zapcore.PanicLevel,
	"fatal":  zapcore.FatalLevel,
}

// formatMessage 根据安全模式格式化消息
func formatMessage(msg string, args []any, isAsync bool) string {
	if len(args) == 0 {
		return msg
	}

	// 根据安全模式决定使用哪种格式化方式
	if shouldUseSafeFormat(isAsync) {
		// 使用安全格式化
		return SafeFormat(msg, args...)
	}

	// 使用高性能格式化
	var sb strings.Builder
	if err := formatToStringBuilder(&sb, msg, args...); err != nil {
		// 格式化失败时返回原始消息
		return msg
	}
	return sb.String()
}

func zapUpdateLevel(logLevel string) {
	// 解析日志级别
	level, err := zapcore.ParseLevel(logLevel)
	if err != nil {
		// 如果解析失败，使用默认的 info 级别
		level = zapcore.InfoLevel
		// 仅输出到 stderr，避免产生日志噪音
		fmt.Fprintf(os.Stderr, "[mlog] 日志级别解析失败: %s, 使用默认 info 级别\n", logLevel)
		return
	}

	// 更新 zapConfig 配置（使用锁保护并发写入）
	globalMutex.Lock()
	zapConfig.Level = logLevel
	globalMutex.Unlock()

	// 使用原子级别控制器动态更新日志级别
	atomicLevel.SetLevel(level)

	// 更新级别缓存映射（使用锁保护）
	levelCacheMutex.Lock()
	levelCache[logLevel] = level
	levelCacheMutex.Unlock()

	// 仅在 Debug 级别记录级别更新（减少日志噪音）
	if atomicLevel.Level() <= zapcore.DebugLevel {
		logger, ok := getLogger()
		if ok {
			// 为 UpdateLevel 调用创建带有正确 caller skip 的 logger
			// 调用栈：用户代码 -> mlog.UpdateLevel() -> zapUpdateLevel() -> logger.Debug()
			// 需要跳过 2 层：zapUpdateLevel() 和 mlog.UpdateLevel()
			loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(2))
			loggerWithSkip.Debug("日志级别已更新",
				zap.String("level", logLevel),
				zap.Stringer("parsed_level", level))
		}
	}
}

func zapCheckLevel(logLevel string) bool {
	// 使用缓存获取级别，避免重复解析（使用读锁）
	levelCacheMutex.RLock()
	checkLevel, ok := levelCache[logLevel]
	levelCacheMutex.RUnlock()
	
	if !ok {
		// 如果缓存中没有，才进行解析
		parsedLevel, err := zapcore.ParseLevel(logLevel)
		if err != nil {
			return false
		}
		checkLevel = parsedLevel
		// 添加到缓存（使用写锁）
		levelCacheMutex.Lock()
		levelCache[logLevel] = checkLevel
		levelCacheMutex.Unlock()
	}

	// 使用原子级别控制器获取当前级别
	currentLevel := atomicLevel.Level()
	return currentLevel <= checkLevel
}

// zapDebug 调试日志
func zapDebug(msg string, args ...any) {
	//是否开启异步日志
	if isAsyncEnabled() {
		debugAsync(msg, args)
	} else {
		logger, ok := getLogger()
		if !ok {
			ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
			return
		}

		// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
		// 调用栈：用户代码 -> mlog.Debug() -> zapDebug() -> logger.Debug()
		// 需要跳过 2 层：zapDebug() 和 mlog.Debug()
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(2))

		// 格式化消息
		formattedMsg := formatMessage(msg, args, false)
		loggerWithSkip.Debug(formattedMsg)
	}
}

// zapInfo 信息日志
func zapInfo(arg0 string, args ...any) {
	//是否开启异步日志
	if isAsyncEnabled() {
		infoAsync(arg0, args)
	} else {
		logger, ok := getLogger()
		if !ok {
			ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
			return
		}

		// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
		// 调用栈：用户代码 -> mlog.Info() -> zapInfo() -> logger.Info()
		// 需要跳过 2 层：zapInfo() 和 mlog.Info()
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(2))

		// 格式化消息
		formattedMsg := formatMessage(arg0, args, false)
		loggerWithSkip.Info(formattedMsg)
	}
}

// zapWarn 警告日志
func zapWarn(arg0 string, args ...any) {
	//是否开启异步日志
	if isAsyncEnabled() {
		warnAsync(arg0, args)
	} else {
		logger, ok := getLogger()
		if !ok {
			ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
			return
		}

		// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
		// 调用栈：用户代码 -> mlog.Warn() -> zapWarn() -> logger.Warn()
		// 需要跳过 2 层：zapWarn() 和 mlog.Warn()
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(2))

		// 格式化消息
		formattedMsg := formatMessage(arg0, args, false)
		loggerWithSkip.Warn(formattedMsg)
	}
}

// zapError 错误日志
func zapError(arg0 string, args ...any) {
	//是否开启异步日志
	if isAsyncEnabled() {
		errorAsync(arg0, args)
	} else {
		logger, ok := getLogger()
		if !ok {
			ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
			return
		}

		// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
		// 调用栈：用户代码 -> mlog.Error() -> zapError() -> logger.Error()
		// 需要跳过 2 层：zapError() 和 mlog.Error()
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(2))

		// 格式化消息
		formattedMsg := formatMessage(arg0, args, false)
		loggerWithSkip.Error(formattedMsg)
	}
}

// zapReturnError 返回错误
func zapReturnError(arg0 string, args ...any) error {
	zapError(arg0, args...)

	// 优化的错误消息格式化
	if len(args) == 0 {
		// 无参数情况，直接使用原始字符串
		return errors.New(arg0)
	}

	// 有参数情况，使用字符串构建器
	var sb strings.Builder

	// 使用更高效的格式化方法
	if err := formatToStringBuilder(&sb, arg0, args...); err != nil {
		// 格式化失败时的回退策略
		return errors.New(arg0)
	}

	return errors.New(sb.String())
}

func formatToStringBuilder(sb *strings.Builder, format string, args ...any) error {
	// 如果没有格式化占位符，直接拼接
	if !strings.Contains(format, "%") {
		sb.WriteString(format)
		for _, arg := range args {
			sb.WriteByte(' ')
			sb.WriteString(fmt.Sprint(arg))
		}
		return nil
	}

	// 对于简单的格式化模式，使用优化的实现
	if len(args) == 1 {
		switch format {
		case "%s":
			if s, ok := args[0].(string); ok {
				sb.WriteString(s)
				return nil
			}
		case "%d":
			if i, ok := args[0].(int); ok {
				sb.WriteString(strconv.Itoa(i))
				return nil
			}
			if i, ok := args[0].(int64); ok {
				sb.WriteString(strconv.FormatInt(i, 10))
				return nil
			}
		case "%v":
			sb.WriteString(fmt.Sprint(args[0]))
			return nil
		}
	}

	// 回退到标准格式化
	formatted := fmt.Sprintf(format, args...)
	sb.WriteString(formatted)
	return nil
}
