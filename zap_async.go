package mlog

import (
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalAsyncLogger *AsyncLogger //异步日志
	asyncMutex        sync.RWMutex //异步日志锁
)

// AsyncLogEntry 异步日志条目
type AsyncLogEntry struct {
	Level     zapcore.Level
	Message   string
	Fields    []zap.Field
	Extras    []any
	Caller    zapcore.EntryCaller // 保存原始调用者信息
	Timestamp time.Time           // 日志产生时的时间戳
}

// OptimizedSkipCache 优化的调用栈跳过层数缓存
type OptimizedSkipCache struct {
	cache   sync.Map // 使用sync.Map提高并发性能
	maxSize int64    // 最大缓存大小
	size    int64    // 当前缓存大小

	// 性能监控指标
	hits   int64
	misses int64
}

// StringBuilderPool 字符串构建器对象池
type StringBuilderPool struct {
	pool sync.Pool
}

// NewStringBuilderPool 创建字符串构建器池
func NewStringBuilderPool() *StringBuilderPool {
	return &StringBuilderPool{
		pool: sync.Pool{
			New: func() interface{} {
				sb := &strings.Builder{}
				sb.Grow(256) // 预分配256字节
				return sb
			},
		},
	}
}

// Get 获取字符串构建器
func (p *StringBuilderPool) Get() *strings.Builder {
	return p.pool.Get().(*strings.Builder)
}

// Put 归还字符串构建器
func (p *StringBuilderPool) Put(sb *strings.Builder) {
	sb.Reset()
	p.pool.Put(sb)
}

// LevelCache 日志级别缓存
type LevelCache struct {
	debugEnabled int32
	infoEnabled  int32
	warnEnabled  int32
	errorEnabled int32
}

// NewLevelCache 创建新的级别缓存
func NewLevelCache() *LevelCache {
	lc := &LevelCache{
		// 默认启用所有级别，避免初始化时的问题
		debugEnabled: 1,
		infoEnabled:  1,
		warnEnabled:  1,
		errorEnabled: 1,
	}
	// 尝试更新缓存，如果logger还没初始化就使用默认值
	lc.updateCache()
	return lc
}

// updateCache 更新级别缓存
func (lc *LevelCache) updateCache() {
	// 使用优化的 logger 获取方式，避免竞态条件
	logger := getLoggerOptimized()
	if logger == nil {
		return
	}

	core := logger.Core()
	// 使用原子操作更新所有级别缓存
	atomic.StoreInt32(&lc.debugEnabled, boolToInt32(core.Enabled(zapcore.DebugLevel)))
	atomic.StoreInt32(&lc.infoEnabled, boolToInt32(core.Enabled(zapcore.InfoLevel)))
	atomic.StoreInt32(&lc.warnEnabled, boolToInt32(core.Enabled(zapcore.WarnLevel)))
	atomic.StoreInt32(&lc.errorEnabled, boolToInt32(core.Enabled(zapcore.ErrorLevel)))
}

// isDebugEnabled 检查Debug级别是否启用
func (lc *LevelCache) isDebugEnabled() bool {
	return atomic.LoadInt32(&lc.debugEnabled) == 1
}

// isInfoEnabled 检查Info级别是否启用
func (lc *LevelCache) isInfoEnabled() bool {
	return atomic.LoadInt32(&lc.infoEnabled) == 1
}

// isWarnEnabled 检查Warn级别是否启用
func (lc *LevelCache) isWarnEnabled() bool {
	return atomic.LoadInt32(&lc.warnEnabled) == 1
}

// isErrorEnabled 检查Error级别是否启用
func (lc *LevelCache) isErrorEnabled() bool {
	return atomic.LoadInt32(&lc.errorEnabled) == 1
}

// isLevelEnabled 检查指定级别是否启用
func (lc *LevelCache) isLevelEnabled(level zapcore.Level) bool {
	switch level {
	case zapcore.DebugLevel:
		return lc.isDebugEnabled()
	case zapcore.InfoLevel:
		return lc.isInfoEnabled()
	case zapcore.WarnLevel:
		return lc.isWarnEnabled()
	case zapcore.ErrorLevel:
		return lc.isErrorEnabled()
	default:
		// 对于其他级别，直接检查
		logger, ok := getLogger()
		if !ok {
			return false
		}
		return logger.Core().Enabled(level)
	}
}

// boolToInt32 将bool转换为int32
func boolToInt32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// AsyncLogger 异步日志器
type AsyncLogger struct {
	logChan    chan AsyncLogEntry
	done       chan struct{}
	wg         sync.WaitGroup
	dropOnFull bool
	skipCache  *OptimizedSkipCache
	sbPool     *StringBuilderPool // 字符串构建器池
	levelCache *LevelCache        // 级别检查缓存
}

// NewOptimizedSkipCache 创建新的优化缓存
func NewOptimizedSkipCache(maxSize int64) *OptimizedSkipCache {
	return &OptimizedSkipCache{
		maxSize: maxSize,
	}
}

// Get 获取缓存值
func (c *OptimizedSkipCache) Get(pc uintptr) (int, bool) {
	if value, ok := c.cache.Load(pc); ok {
		atomic.AddInt64(&c.hits, 1)
		return value.(int), true
	}
	atomic.AddInt64(&c.misses, 1)
	return 0, false
}

// Set 设置缓存值
func (c *OptimizedSkipCache) Set(pc uintptr, skip int) {
	// 简单的大小控制：如果缓存未满，则添加
	if atomic.LoadInt64(&c.size) < c.maxSize {
		if _, loaded := c.cache.LoadOrStore(pc, skip); !loaded {
			atomic.AddInt64(&c.size, 1)
		}
	}
}

// GetStats 获取缓存统计信息
func (c *OptimizedSkipCache) GetStats() (hits, misses int64, size int64, hitRate float64) {
	hits = atomic.LoadInt64(&c.hits)
	misses = atomic.LoadInt64(&c.misses)
	size = atomic.LoadInt64(&c.size)

	total := hits + misses
	if total > 0 {
		hitRate = float64(hits) / float64(total)
	}

	return hits, misses, size, hitRate
}

// Clear 清空缓存（用于测试或重置）
func (c *OptimizedSkipCache) Clear() {
	c.cache.Range(func(key, value interface{}) bool {
		c.cache.Delete(key)
		return true
	})
	atomic.StoreInt64(&c.size, 0)
	atomic.StoreInt64(&c.hits, 0)
	atomic.StoreInt64(&c.misses, 0)
}

// newAsyncLogger 创建新的异步日志器
func newAsyncLogger(bufferSize int, dropOnFull bool) *AsyncLogger {
	al := &AsyncLogger{
		logChan:    make(chan AsyncLogEntry, bufferSize),
		done:       make(chan struct{}),
		dropOnFull: dropOnFull,
		skipCache:  NewOptimizedSkipCache(1000), // 默认最大1000个缓存条目
		sbPool:     NewStringBuilderPool(),      // 初始化字符串构建器池
		levelCache: NewLevelCache(),             // 初始化级别检查缓存
	}

	al.wg.Add(1)
	go al.processLogs()
	return al
}

// processLogEntry 处理单个日志条目（优化版本）
func (al *AsyncLogger) processLogEntry(entry AsyncLogEntry) {
	logger, ok := getLogger()
	if !ok {
		return
	}

	// 【并发安全修复】消息已经在发送前格式化完成，这里不再需要处理 Extras
	// entry.Message 已经是格式化后的最终消息

	// 直接使用zapcore写入日志条目，保持原始caller信息
	if entry.Caller.Defined {
		al.writeLogEntryWithCaller(logger, entry)
	} else {
		// 回退到普通日志记录
		al.writeLogEntryFallback(logger, entry)
	}
}

// processLogs 处理异步日志（优化版本）
func (al *AsyncLogger) processLogs() {
	defer al.wg.Done()

	for {
		select {
		case entry := <-al.logChan:
			al.processLogEntry(entry)
		case <-al.done:
			// 处理剩余的日志
			al.drainRemainingLogs()
			return
		}
	}
}

// drainRemainingLogs 处理剩余的日志
func (al *AsyncLogger) drainRemainingLogs() {
	for {
		select {
		case entry := <-al.logChan:
			al.processLogEntry(entry)
		default:
			return
		}
	}
}

// writeLogEntryFallback 回退的日志写入方法
func (al *AsyncLogger) writeLogEntryFallback(logger *zap.Logger, entry AsyncLogEntry) {
	switch entry.Level {
	case zapcore.DebugLevel:
		logger.Debug(entry.Message, entry.Fields...)
	case zapcore.InfoLevel:
		logger.Info(entry.Message, entry.Fields...)
	case zapcore.WarnLevel:
		logger.Warn(entry.Message, entry.Fields...)
	case zapcore.ErrorLevel:
		logger.Error(entry.Message, entry.Fields...)
	case zapcore.DPanicLevel:
		logger.DPanic(entry.Message, entry.Fields...)
	case zapcore.PanicLevel:
		logger.Panic(entry.Message, entry.Fields...)
	case zapcore.FatalLevel:
		logger.Fatal(entry.Message, entry.Fields...)
	}
}

// logAsync 异步记录日志
func (al *AsyncLogger) logAsync(level zapcore.Level, msg string, args []any, fields ...zap.Field) {
	al.logAsyncWithSkip(level, msg, args, 3, fields...) // 默认skip 3层调用栈
}

// logAsyncWithSkip 异步记录日志，指定调用栈跳过层数
func (al *AsyncLogger) logAsyncWithSkip(level zapcore.Level, msg string, args []any, skip int, fields ...zap.Field) {
	// 快速级别检查，避免不必要的处理
	if !al.levelCache.isLevelEnabled(level) {
		return
	}

	// 【关键修复】在日志产生时立即捕获时间戳
	// 这确保时间戳反映的是日志产生的真实时间，而非异步处理时的时间
	timestamp := time.Now()

	// 动态检测调用路径并调整skip值
	adjustedSkip := al.detectAndAdjustSkip(skip)

	// 在进入异步队列之前捕获caller信息
	caller := zapcore.NewEntryCaller(uintptr(0), "", 0, false)
	if pc, file, line, ok := runtime.Caller(adjustedSkip); ok {
		caller = zapcore.NewEntryCaller(pc, file, line, true)
	}

	// 【并发安全修复 - 安全格式化方案】
	// 使用 SafeFormatter 进行安全的参数序列化
	// 这个方案会将所有参数转换为不可变的形式，完全避免并发问题
	//
	// 优势：
	// 1. 完全避免 "concurrent map iteration and map write" fatal error
	// 2. 不依赖用户的并发安全保证
	// 3. 对于 map 类型，会立即创建快照（JSON 序列化）
	// 4. 对于其他复杂类型，也会进行安全的转换
	formattedMsg := SafeFormat(msg, args...)

	entry := AsyncLogEntry{
		Level:     level,
		Message:   formattedMsg,
		Fields:    fields,
		Extras:    nil,       // 已经格式化完成，不再需要传递原始参数
		Caller:    caller,    // 保存原始调用者信息
		Timestamp: timestamp, // 保存日志产生时的时间戳
	}

	if al.dropOnFull {
		select {
		case al.logChan <- entry:
		default:
			// 缓冲区满时丢弃日志
		}
	} else {
		select {
		case al.logChan <- entry:
		case <-al.done:
			// 如果正在关闭，直接返回
			return
		}
	}
}

// detectAndAdjustSkip 动态检测调用路径并调整skip值（优化缓存版本）
func (al *AsyncLogger) detectAndAdjustSkip(skip int) int {
	// 获取调用者的PC值作为缓存键
	if pc, _, _, ok := runtime.Caller(2); ok { // skip=2 跳过当前函数和logAsyncWithSkip
		// 先检查缓存
		if cachedSkip, exists := al.skipCache.Get(pc); exists {
			return cachedSkip
		}

		// 缓存未命中，进行检测
		adjustedSkip := al.detectSkipSlow(skip)

		// 更新缓存
		al.skipCache.Set(pc, adjustedSkip)

		return adjustedSkip
	}

	// 如果无法获取PC值，回退到慢速检测
	return al.detectSkipSlow(skip)
}

// detectSkipSlow 慢速路径：遍历调用栈检测
func (al *AsyncLogger) detectSkipSlow(skip int) int {
	// 检查调用栈中是否包含zapDebug、zapInfo等函数
	for i := 0; i < 8; i++ { // 减少循环次数从10到8
		if pc, _, _, ok := runtime.Caller(i); ok {
			fn := runtime.FuncForPC(pc)
			if fn != nil {
				funcName := fn.Name()
				// 使用更高效的字符串匹配
				if al.isZapFunction(funcName) {
					return skip + 1
				}
			}
		}
	}
	// 如果没有找到zap*函数，说明是通过DebugW、InfoW等直接调用的
	return skip
}

// isZapFunction 检查是否为zap函数（优化字符串匹配）
func (al *AsyncLogger) isZapFunction(funcName string) bool {
	// 使用更高效的字符串匹配，避免多次调用strings.Contains
	if len(funcName) < 7 { // "zapDebug"最短7个字符
		return false
	}

	// 查找"zap"子串的位置
	zapIndex := strings.Index(funcName, "zap")
	if zapIndex == -1 {
		return false
	}

	// 检查zap后面是否跟着Debug、Info、Warn、Error
	remaining := funcName[zapIndex+3:]
	return strings.HasPrefix(remaining, "Debug") ||
		strings.HasPrefix(remaining, "Info") ||
		strings.HasPrefix(remaining, "Warn") ||
		strings.HasPrefix(remaining, "Error")
}

// writeLogEntryWithCaller 使用保存的caller信息写入日志条目
func (al *AsyncLogger) writeLogEntryWithCaller(logger *zap.Logger, entry AsyncLogEntry) {
	// 创建zapcore.Entry，使用保存的caller信息和时间戳
	zapEntry := zapcore.Entry{
		Level:      entry.Level,
		Time:       entry.Timestamp, // 【关键修复】使用日志产生时的时间戳，而非写入时的时间
		LoggerName: "",
		Message:    entry.Message,
		Caller:     entry.Caller,
		Stack:      "",
	}

	// 获取logger的core并直接写入
	if ce := logger.Core().Check(zapEntry, nil); ce != nil {
		ce.Write(entry.Fields...)
	}
}

// GetCacheStats 获取缓存统计信息
func (al *AsyncLogger) GetCacheStats() (hits, misses int64, size int64, hitRate float64) {
	return al.skipCache.GetStats()
}

// ClearCache 清空缓存（用于测试或重置）
func (al *AsyncLogger) ClearCache() {
	al.skipCache.Clear()
}

// UpdateLevelCache 更新级别缓存
func (al *AsyncLogger) UpdateLevelCache() {
	al.levelCache.updateCache()
}

// Close 关闭异步日志器
func (al *AsyncLogger) Close() {
	close(al.done)
	al.wg.Wait()
}

// close 关闭异步日志器（向后兼容）
func (al *AsyncLogger) close() {
	al.Close()
}

// debugAsync 异步调试日志
func (al *AsyncLogger) debugAsync(msg string, args []any, fields ...zap.Field) {
	// 调用栈：用户代码 -> mlog.Debug() -> zapDebug() -> debugAsync() -> al.debugAsync() -> al.logAsyncWithSkip()
	// 需要跳过 5 层才能到达用户代码
	al.logAsyncWithSkip(zapcore.DebugLevel, msg, args, 5, fields...)
}

// infoAsync 异步信息日志
func (al *AsyncLogger) infoAsync(msg string, args []any, fields ...zap.Field) {
	// 调用栈：用户代码 -> mlog.Info() -> zapInfo() -> infoAsync() -> al.infoAsync() -> al.logAsyncWithSkip()
	// 需要跳过 5 层才能到达用户代码
	al.logAsyncWithSkip(zapcore.InfoLevel, msg, args, 5, fields...)
}

// warnAsync 异步警告日志
func (al *AsyncLogger) warnAsync(msg string, args []any, fields ...zap.Field) {
	// 调用栈：用户代码 -> mlog.Warn() -> zapWarn() -> warnAsync() -> al.warnAsync() -> al.logAsyncWithSkip()
	// 需要跳过 5 层才能到达用户代码
	al.logAsyncWithSkip(zapcore.WarnLevel, msg, args, 5, fields...)
}

// errorAsync 异步错误日志
func (al *AsyncLogger) errorAsync(msg string, args []any, fields ...zap.Field) {
	// 调用栈：用户代码 -> mlog.Error() -> zapError() -> errorAsync() -> al.errorAsync() -> al.logAsyncWithSkip()
	// 需要跳过 5 层才能到达用户代码
	al.logAsyncWithSkip(zapcore.ErrorLevel, msg, args, 5, fields...)
}

// getAsyncLogger 安全地获取全局异步日志器
func getAsyncLogger() (*AsyncLogger, bool) {
	asyncMutex.RLock()
	defer asyncMutex.RUnlock()
	return globalAsyncLogger, globalAsyncLogger != nil
}

// debugAsync 异步调试日志（全局函数）
func debugAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		// 调试代码已移除

		// 使用基础skip值3，detectAndAdjustSkip会根据调用栈动态调整
		// 调用栈：用户代码 -> mlog.DebugW()/Debug() -> [zapDebug()] -> debugAsync() -> logger.logAsyncWithSkip()
		// 基础skip=3，如果有zapDebug会自动+1变成4
		logger.logAsyncWithSkip(zapcore.DebugLevel, msg, args, 3, fields...)
	} else {
		// 如果异步日志器未启用，回退到同步日志
		DebugW(msg, fields...)
	}
}

// infoAsync 异步信息日志（全局函数）
func infoAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		// 使用基础skip值3，detectAndAdjustSkip会根据调用栈动态调整
		logger.logAsyncWithSkip(zapcore.InfoLevel, msg, args, 3, fields...)
	} else {
		// 如果异步日志器未启用，回退到同步日志
		InfoW(msg, fields...)
	}
}

// warnAsync 异步警告日志（全局函数）
func warnAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		// 使用基础skip值3，detectAndAdjustSkip会根据调用栈动态调整
		logger.logAsyncWithSkip(zapcore.WarnLevel, msg, args, 3, fields...)
	} else {
		// 如果异步日志器未启用，回退到同步日志
		WarnW(msg, fields...)
	}
}

// errorAsync 异步错误日志（全局函数）
func errorAsync(msg string, args []any, fields ...zap.Field) {
	if logger, ok := getAsyncLogger(); ok {
		// 使用基础skip值3，detectAndAdjustSkip会根据调用栈动态调整
		logger.logAsyncWithSkip(zapcore.ErrorLevel, msg, args, 3, fields...)
	} else {
		// 如果异步日志器未启用，回退到同步日志
		ErrorW(msg, fields...)
	}
}

// GetAsyncCacheStats 获取全局异步日志器的缓存统计信息
func GetAsyncCacheStats() (hits, misses int64, size int64, hitRate float64) {
	if logger, ok := getAsyncLogger(); ok {
		return logger.GetCacheStats()
	}
	return 0, 0, 0, 0
}

// ClearAsyncCache 清空全局异步日志器的缓存
func ClearAsyncCache() {
	if logger, ok := getAsyncLogger(); ok {
		logger.ClearCache()
	}
}

// UpdateAsyncLevelCache 更新全局异步日志器的级别缓存
func UpdateAsyncLevelCache() {
	// 使用读锁安全地获取异步日志器
	asyncMutex.RLock()
	logger := globalAsyncLogger
	asyncMutex.RUnlock()

	if logger != nil {
		logger.UpdateLevelCache()
	}
}

// isAsyncEnabled 检查异步日志是否启用
func isAsyncEnabled() bool {
	_, enabled := getAsyncLogger()
	return enabled
}
