package mlog

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"
	"unsafe"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	stopFlag    int32
	stopNetFlag int32
	zapConfig   ZapConfig
	atomicLevel zap.AtomicLevel
	initialized int32
	// 优化的无锁logger访问
	loggerPtr unsafe.Pointer // *zap.Logger，使用unsafe.Pointer实现无锁访问
	// 优化的日志级别缓存（原子操作）
	debugEnabledCache int32
	infoEnabledCache  int32
	warnEnabledCache  int32
	errorEnabledCache int32
)

func InitialZap(name string, id uint64, logLevel string, zc ZapConfig) {
	globalMutex.Lock()
	defer globalMutex.Unlock()

	// 如果已经初始化，先关闭现有的日志器
	if atomic.LoadInt32(&initialized) == 1 {
		if logger := (*zap.Logger)(atomic.LoadPointer(&loggerPtr)); logger != nil {
			logger.Sync() // 确保所有日志都被写入
		}
		if zapLogger != nil {
			zapLogger.Sync() // 兼容性：同时同步旧的logger
		}
	}

	zapConfig = zc

	// 如果提供了 logLevel 参数，优先使用它
	finalLevel := zapConfig.Level
	if logLevel != "" {
		finalLevel = logLevel
		zapConfig.Level = logLevel
	}

	// 初始化原子级别控制器
	level, err := zapcore.ParseLevel(finalLevel)
	if err != nil {
		level = zapcore.InfoLevel
	}
	atomicLevel = zap.NewAtomicLevelAt(level)

	// 更新优化的日志级别缓存
	updateLevelCacheOptimized(atomicLevel.Level())

	// 初始化zap日志库
	logger := initZap(name, id)

	// 原子更新logger指针（无锁访问）
	atomic.StorePointer(&loggerPtr, unsafe.Pointer(logger))

	// 兼容性：保持旧的全局变量
	zapLogger = logger
	zap.ReplaceGlobals(logger)

	// 初始化异步日志器（如果启用）
	if zapConfig.EnableAsync {
		asyncMutex.Lock()
		// 关闭现有的异步日志器
		if globalAsyncLogger != nil {
			globalAsyncLogger.close()
		}

		// 设置默认值
		bufferSize := zapConfig.AsyncBufferSize
		if bufferSize <= 0 {
			bufferSize = 10000 // 默认缓冲区大小
		}

		globalAsyncLogger = newAsyncLogger(bufferSize, zapConfig.AsyncDropOnFull)
		asyncMutex.Unlock()
	}
	// 初始化路径缓存（如果启用）
	if zapConfig.UseRelativePath {
		initPathCache()
		// 如果配置了编译根目录，更新缓存
		if zapConfig.BuildRootPath != "" {
			updateBuildRoot(zapConfig.BuildRootPath)
		}
	}

	// 标记为已初始化
	atomic.StoreInt32(&initialized, 1)
}

func GLOG() *zap.Logger {
	return getLoggerOptimized()
}

func updateLevelCacheOptimized(currentLevel zapcore.Level) {
	// 使用原子操作更新级别缓存
	// 注意：zapcore.Level 的值：Debug=-1, Info=0, Warn=1, Error=2
	// 当设置的级别 <= 某个级别时，该级别应该被启用
	if currentLevel <= zapcore.DebugLevel {
		atomic.StoreInt32(&debugEnabledCache, 1)
	} else {
		atomic.StoreInt32(&debugEnabledCache, 0)
	}

	if currentLevel <= zapcore.InfoLevel {
		atomic.StoreInt32(&infoEnabledCache, 1)
	} else {
		atomic.StoreInt32(&infoEnabledCache, 0)
	}

	if currentLevel <= zapcore.WarnLevel {
		atomic.StoreInt32(&warnEnabledCache, 1)
	} else {
		atomic.StoreInt32(&warnEnabledCache, 0)
	}

	if currentLevel <= zapcore.ErrorLevel {
		atomic.StoreInt32(&errorEnabledCache, 1)
	} else {
		atomic.StoreInt32(&errorEnabledCache, 0)
	}
}

func getLoggerOptimized() *zap.Logger {
	if atomic.LoadInt32(&initialized) == 0 {
		return nil
	}
	return (*zap.Logger)(atomic.LoadPointer(&loggerPtr))
}

func getLogger() (*zap.Logger, bool) {
	logger := getLoggerOptimized()
	return logger, logger != nil
}

// isDebugEnabledFast 快速检查Debug级别是否启用
func isDebugEnabledFast() bool {
	return atomic.LoadInt32(&debugEnabledCache) == 1
}

// isInfoEnabledFast 快速检查Info级别是否启用
func isInfoEnabledFast() bool {
	return atomic.LoadInt32(&infoEnabledCache) == 1
}

// isWarnEnabledFast 快速检查Warn级别是否启用
func isWarnEnabledFast() bool {
	return atomic.LoadInt32(&warnEnabledCache) == 1
}

// isErrorEnabledFast 快速检查Error级别是否启用
func isErrorEnabledFast() bool {
	return atomic.LoadInt32(&errorEnabledCache) == 1
}

// isInitialized 检查日志系统是否已初始化
func isInitialized() bool {
	return atomic.LoadInt32(&initialized) == 1
}

// UpdateLevel 动态更新日志级别
func UpdateLevel(logLevel string) {
	zapUpdateLevel(logLevel)
	// 更新优化的级别缓存
	if atomicLevel.Level() != zapcore.InvalidLevel {
		updateLevelCacheOptimized(atomicLevel.Level())
	}
}

// CheckLevel 检查指定的日志级别是否有效
func CheckLevel(logLevel string) bool {
	return zapCheckLevel(logLevel)
}

// Close 关闭日志系统
func Close() {
	// 关闭异步日志器
	asyncMutex.Lock()
	if globalAsyncLogger != nil {
		globalAsyncLogger.close()
		globalAsyncLogger = nil
	}
	asyncMutex.Unlock()

	// 关闭同步日志器（使用优化的获取方式）
	logger := getLoggerOptimized()
	if logger != nil {
		// 智能同步：只对文件输出进行同步，避免 stdout/stderr 同步错误
		if err := syncLoggerSafely(logger); err != nil {
			// 只有在真正的错误情况下才输出错误信息
			fmt.Fprintf(os.Stderr, "日志同步失败: %v\n", err)
		}
	}

	// 清理优化的logger指针
	atomic.StorePointer(&loggerPtr, nil)

	// 重置初始化标志
	atomic.StoreInt32(&initialized, 0)
}

// Debug 输出调试级别日志 兼容
func Debug(msg string, args ...any) {
	// 快速预检查，避免不必要的处理
	if !isDebugEnabledFast() {
		return
	}
	// 有参数时使用原有的格式化逻辑
	zapDebug(msg, args...)
}

// DebugW 输出带结构化字段的调试级别日志
func DebugW(msg string, fields ...zap.Field) {
	// 快速预检查，避免不必要的处理
	if !isDebugEnabledFast() {
		return
	}
	// 检查是否使用异步模式
	if isAsyncEnabled() {
		debugAsync(msg, nil, fields...)
		return
	}
	// 获取日志构造器
	logger := getLoggerOptimized()
	if logger == nil {
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.DebugW() -> logger.Debug()
	// 需要跳过 1 层：mlog.DebugW()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
	loggerWithSkip.Debug(msg, fields...)
}

// Info 输出信息级别日志
func Info(msg string, args ...any) {
	// 快速预检查，避免不必要的处理
	if !isInfoEnabledFast() {
		return
	}
	// 有参数时使用原有的格式化逻辑
	zapInfo(msg, args...)
}

// InfoW 输出带结构化字段的信息级别日志
func InfoW(msg string, fields ...zap.Field) {
	// 快速预检查
	if !isInfoEnabledFast() {
		return
	}
	// 检查是否使用异步模式
	if isAsyncEnabled() {
		infoAsync(msg, nil, fields...)
		return
	}
	// 获取日志构造器
	logger := getLoggerOptimized()
	if logger == nil {
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.InfoW() -> logger.Info()
	// 需要跳过 1 层：mlog.InfoW()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
	loggerWithSkip.Info(msg, fields...)
}

func Warn(msg string, args ...any) {
	// 快速预检查，避免不必要的处理
	if !isWarnEnabledFast() {
		return
	}
	// 有参数时使用原有的格式化逻辑
	zapWarn(msg, args...)
}

func WarnW(msg string, fields ...zap.Field) {
	// 快速预检查
	if !isWarnEnabledFast() {
		return
	}
	if isAsyncEnabled() {
		warnAsync(msg, nil, fields...)
		return
	}
	// 获取日志构造器
	logger := getLoggerOptimized()
	if logger == nil {
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.WarnW() -> logger.Warn()
	// 需要跳过 1 层：mlog.WarnW()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
	loggerWithSkip.Warn(msg, fields...)
}

func Error(arg0 string, args ...interface{}) {
	// 快速预检查，避免不必要的处理
	if !isErrorEnabledFast() {
		return
	}
	// 有参数时使用原有的格式化逻辑
	zapError(arg0, args...)
}

// ErrorW 输出带结构化字段的错误级别日志
func ErrorW(msg string, fields ...zap.Field) {
	// 快速预检查
	if !isErrorEnabledFast() {
		return
	}

	// 检查是否使用异步模式
	if isAsyncEnabled() {
		errorAsync(msg, nil, fields...)
		return
	}
	logger := getLoggerOptimized()
	if logger == nil {
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.ErrorW() -> logger.Error()
	// 需要跳过 1 层：mlog.ErrorW()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
	loggerWithSkip.Error(msg, fields...)
}

// ReturnError 输出错误日志并返回error对象
func ReturnError(msg string, args ...any) error {
	return zapReturnError(msg, args...)
}

// Lock 输出锁定相关的日志
func Lock(msg string, args ...any) {
	logger, ok := getLogger()
	if !ok {
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.Lock() -> logger.Info()
	// 需要跳过 1 层：mlog.Lock()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))

	if len(args) == 0 {
		// 无参数情况，直接拼接前缀
		loggerWithSkip.Info(msg, zap.String("directory", "concurrent"))
		return
	}
	// 有参数情况，使用字符串构建器
	var sb strings.Builder
	// 使用更高效的格式化方法
	if err := formatToStringBuilder(&sb, msg, args...); err != nil {
		// 格式化失败时的回退策略
		loggerWithSkip.Info(msg, zap.Error(err), zap.String("directory", "concurrent"))
		return
	}
	loggerWithSkip.Info(sb.String(), zap.String("directory", "concurrent"))
}

// Critical 输出严重错误日志
// 紧急情况下的警告日志，问题严重，不至于要立刻处理
func Critical(msg string, args ...any) {
	logger, ok := getLogger()
	if !ok {
		// 避免无限递归，直接 panic 而不调用 ExitGame
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.Critical() -> logger.Warn()
	// 需要跳过 1 层：mlog.Critical()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))

	if len(args) == 0 {
		// 无参数情况，直接拼接前缀
		loggerWithSkip.Warn(msg, zap.String("directory", "emergency"))
		return
	}
	// 有参数情况，使用字符串构建器
	var sb strings.Builder
	if err := formatToStringBuilder(&sb, msg, args...); err != nil {
		loggerWithSkip.Warn(msg, zap.Error(err), zap.String("directory", "emergency"))
		return
	}
	loggerWithSkip.Warn(sb.String(), zap.String("directory", "emergency"))
}

// Disaster 输出最严重的数据问题日志
// 紧急情况下的错误日志，问题严重，需要立刻处理
func Disaster(msg string, args ...interface{}) {
	logger, ok := getLogger()
	if !ok {
		ExitGame("zapLogger 还没有初始化，请先调用 InitialZap")
		return
	}

	// 为 mlog 包装函数调用创建带有正确 caller skip 的 logger
	// 调用栈：用户代码 -> mlog.Disaster() -> logger.Error()
	// 需要跳过 1 层：mlog.Disaster()
	loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))

	if len(args) == 0 {
		// 无参数情况，直接拼接前缀
		loggerWithSkip.Error(msg, zap.String("directory", "emergency"))
		return
	}
	// 有参数情况，使用字符串构建器
	var sb strings.Builder
	if err := formatToStringBuilder(&sb, msg, args...); err != nil {
		// 格式化失败时的回退策略
		loggerWithSkip.Error(msg, zap.Error(err), zap.String("directory", "emergency"))
		return
	}
	loggerWithSkip.Error(sb.String())
}

// ExitGame 输出严重错误并退出游戏
func ExitGame(format string, args ...any) {
	if !isInitialized() {
		panic(fmt.Sprintf(format, args...))
	}
	// 优化的消息构建
	var msg string
	if len(args) == 0 {
		msg = format
	} else {
		msg = fmt.Sprintf(format, args...)
	}

	if StopFlag() {
		// 优化：直接构建警告消息，避免额外的格式化
		Warn("[ExitGame] Has stopped,%s", msg)
		return
	}
	// 优化：直接传递消息，避免额外的格式化
	Disaster("%s", msg)
	time.Sleep(3000 * time.Millisecond)
	panic(msg)
}

// GrpcAssert 输出GRPC断言信息（优化版本：保持堆栈信息完整性以支持IDE跳转）
func GrpcAssert(format string, args ...any) {
	// 快速预检查，避免不必要的处理
	if !isInfoEnabledFast() {
		return
	}

	// 优化的消息构建
	_, src, line, _ := runtime.Caller(1)

	// 根据配置决定使用相对路径还是绝对路径
	displayPath := src
	if zapConfig.UseRelativePath {
		displayPath = getRelativePath(src)
	}

	var msg string
	if len(args) == 0 {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, format)
	} else {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, fmt.Sprintf(format, args...))
	}

	// 获取堆栈信息
	buf := debug.Stack()
	stringStack := BytesToString(buf)

	// 根据配置处理堆栈信息中的路径
	if zapConfig.UseRelativePath {
		stringStack = convertStackPathsToRelative(stringStack)
	}

	// 优化：将堆栈信息作为消息主体，保持完整性以支持IDE跳转
	// 使用格式化的多行消息，在日志文件中有良好的可读性
	stackMessage := fmt.Sprintf("[GrpcAssert] %s\n\nStack Trace:\n%s", msg, stringStack)

	// 直接使用 logger 而不是 InfoW，因为我们已经手动获取了调用信息
	// 调用栈：用户代码 -> mlog.GrpcAssert() -> logger.Info()
	// 需要跳过 1 层：mlog.GrpcAssert()
	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
		loggerWithSkip.Info(stackMessage, zap.String("directory", "assert"))
	}
}

// AssertString 输出断言信息（优化版本：保持堆栈信息完整性以支持IDE跳转）
func AssertString(format string, args ...interface{}) {
	// 快速预检查，避免不必要的处理
	if !isInfoEnabledFast() {
		return
	}

	// 优化的消息构建
	_, src, line, _ := runtime.Caller(1)

	// 根据配置决定使用相对路径还是绝对路径
	displayPath := src
	if zapConfig.UseRelativePath {
		displayPath = getRelativePath(src)
	}

	var msg string
	if len(args) == 0 {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, format)
	} else {
		msg = fmt.Sprintf("%s:%d %s", displayPath, line, fmt.Sprintf(format, args...))
	}

	// 获取堆栈信息
	buf := debug.Stack()
	stringStack := BytesToString(buf)

	// 根据配置处理堆栈信息中的路径
	if zapConfig.UseRelativePath {
		stringStack = convertStackPathsToRelative(stringStack)
	}

	// 优化：将堆栈信息作为消息主体，保持完整性以支持IDE跳转
	// 使用格式化的多行消息，在日志文件中有良好的可读性
	stackMessage := fmt.Sprintf("[Assert] %s\n\nStack Trace:\n%s", msg, stringStack)

	// 直接使用 logger 而不是 InfoW，因为我们已经手动获取了调用信息
	// 调用栈：用户代码 -> mlog.AssertString() -> logger.Info()
	// 需要跳过 1 层：mlog.AssertString()
	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
		loggerWithSkip.Info(stackMessage, zap.String("directory", "assert"))
	}
}

// BytesToString 将字节数组转换为字符串
func BytesToString(p []byte) string {
	// 优化：使用更高效的查找方式
	for i, b := range p {
		if b == 0 {
			return string(p[:i])
		}
	}
	return string(p)
}

// convertStackPathsToRelative 将堆栈信息中的绝对路径转换为相对路径（优化版本）
func convertStackPathsToRelative(stackTrace string) string {
	// 如果全局路径缓存可用且有预编译的正则表达式，使用优化版本
	if globalPathCache != nil && globalPathCache.stackPathRegex != nil {
		return convertStackPathsToRelativeOptimized(stackTrace)
	}

	// 回退到原始实现
	return convertStackPathsToRelativeLegacy(stackTrace)
}

// convertStackPathsToRelativeOptimized 优化的堆栈路径转换
func convertStackPathsToRelativeOptimized(stackTrace string) string {
	// 使用预编译的正则表达式进行批量替换
	return globalPathCache.stackPathRegex.ReplaceAllStringFunc(stackTrace, func(match string) string {
		// 提取路径和行号
		parts := strings.SplitN(match, ":", 2)
		if len(parts) != 2 {
			return match
		}

		filePath := parts[0]
		lineInfo := parts[1]

		// 使用缓存的路径转换
		relativePath := getRelativePath(filePath)
		return relativePath + ":" + lineInfo
	})
}

// convertStackPathsToRelativeLegacy 原始实现（保持兼容性）
func convertStackPathsToRelativeLegacy(stackTrace string) string {
	lines := strings.Split(stackTrace, "\n")
	for i, line := range lines {
		// 查找包含文件路径的行（通常以制表符开头，包含文件路径和行号）
		if strings.Contains(line, "/") && (strings.Contains(line, ".go:") || strings.Contains(line, ".go ")) {
			// 使用正则表达式或字符串处理来替换路径
			lines[i] = replaceAbsolutePathInLine(line)
		}
	}
	return strings.Join(lines, "\n")
}

// replaceAbsolutePathInLine 替换单行中的绝对路径为相对路径（优化版本）
func replaceAbsolutePathInLine(line string) string {
	// 快速检查是否包含需要处理的路径
	if !strings.Contains(line, "/") || !strings.Contains(line, ".go") {
		return line
	}

	// 使用 strings.Builder 减少内存分配
	var result strings.Builder
	result.Grow(len(line)) // 预分配容量

	// 逐字段处理，避免完整分割
	fields := strings.Fields(line)
	for i, field := range fields {
		if i > 0 {
			result.WriteByte(' ')
		}

		// 检查是否是路径字段
		if strings.Contains(field, "/") && strings.Contains(field, ".go") {
			result.WriteString(replacePathInField(field))
		} else {
			result.WriteString(field)
		}
	}

	return result.String()
}

// replacePathInField 替换字段中的路径（优化的辅助函数）
func replacePathInField(field string) string {
	// 查找常见的路径分隔符
	if colonIndex := strings.Index(field, ":"); colonIndex != -1 {
		// 格式：/path/to/file.go:123
		filePath := field[:colonIndex]
		suffix := field[colonIndex:]
		relativePath := getRelativePath(filePath)
		return relativePath + suffix
	}

	if spaceIndex := strings.Index(field, " "); spaceIndex != -1 {
		// 格式：/path/to/file.go +0x123
		filePath := field[:spaceIndex]
		suffix := field[spaceIndex:]
		relativePath := getRelativePath(filePath)
		return relativePath + suffix
	}

	// 只是路径
	return getRelativePath(field)
}

// syncLoggerSafely 安全地同步日志器，避免 stdout/stderr 同步错误
func syncLoggerSafely(logger *zap.Logger) error {
	// 检查当前配置是否输出到控制台
	if zapConfig.LogInConsole {
		// 如果配置为输出到控制台，检查是否为交互式终端
		if !isInteractiveTerminal() {
			// 非交互式终端（如重定向、管道、CI环境），跳过同步
			return nil
		}
	}

	// 尝试同步，但忽略特定的错误
	if err := logger.Sync(); err != nil {
		// 检查是否为已知的无害错误
		if isHarmlessSyncError(err) {
			return nil
		}
		return err
	}

	return nil
}

// isInteractiveTerminal 检查是否为交互式终端
func isInteractiveTerminal() bool {
	// 检查 stdout 是否连接到终端
	if fileInfo, err := os.Stdout.Stat(); err == nil {
		// 如果是字符设备，通常表示连接到终端
		return (fileInfo.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

// isHarmlessSyncError 检查是否为无害的同步错误
func isHarmlessSyncError(err error) bool {
	errStr := err.Error()

	// 常见的无害错误模式
	harmlessPatterns := []string{
		"sync /dev/stdout: inappropriate ioctl for device",
		"sync /dev/stderr: inappropriate ioctl for device",
		"sync /dev/stdout: invalid argument",
		"sync /dev/stderr: invalid argument",
		"sync /dev/stdout: operation not supported",
		"sync /dev/stderr: operation not supported",
	}

	for _, pattern := range harmlessPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// StopFlag 检查停止标志
//
// 返回值:
//   - bool: 是否已设置停止标志
//
// 功能:
//   - 线程安全的停止标志检查
//   - 使用原子操作避免锁竞争
func StopFlag() bool {
	return atomic.LoadInt32(&stopFlag) == 1
}

// SetStopFlag 设置停止标志
//
// 功能:
//   - 线程安全的停止标志设置
//   - 输出设置日志用于调试
func SetStopFlag() {
	// 直接使用 logger 而不是通过 Info() 函数，避免多层调用导致的 caller skip 问题
	// 调用栈：用户代码 -> mlog.SetStopFlag() -> logger.Info()
	// 需要跳过 1 层：mlog.SetStopFlag()
	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
		loggerWithSkip.Info("[SetStopFlag] start")
	}
	atomic.StoreInt32(&stopFlag, 1)
}

// StopNetFlag 检查网络停止标志
//
// 返回值:
//   - bool: 是否已设置网络停止标志
//
// 功能:
//   - 线程安全的网络停止标志检查
//   - 使用原子操作避免锁竞争
func StopNetFlag() bool {
	return atomic.LoadInt32(&stopNetFlag) == 1
}

// SetStopNetFlag 设置网络停止标志
//
// 功能:
//   - 线程安全的网络停止标志设置
//   - 输出设置日志用于调试
func SetStopNetFlag() {
	// 直接使用 logger 而不是通过 Info() 函数，避免多层调用导致的 caller skip 问题
	// 调用栈：用户代码 -> mlog.SetStopNetFlag() -> logger.Info()
	// 需要跳过 1 层：mlog.SetStopNetFlag()
	logger := getLoggerOptimized()
	if logger != nil {
		loggerWithSkip := logger.WithOptions(zap.AddCallerSkip(1))
		loggerWithSkip.Info("[SetStopNetFlag] start")
	}
	atomic.StoreInt32(&stopNetFlag, 1)
}
