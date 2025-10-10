package mlog

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/ai-mmo/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ZapCore struct {
	level       zapcore.Level
	serviceName string // 保存创建时的服务名称
	serviceID   uint64 // 保存创建时的服务ID
	zapcore.Core
	// 添加 lumberjack logger 引用，用于正确关闭
	lumberjackLogger *lumberjack.Logger
	// 缓存编码器，避免重复创建
	encoder zapcore.Encoder
	// 缓存特殊目录的 lumberjack logger，避免重复创建和 goroutine 泄露
	specialLoggers map[string]*lumberjack.Logger
	// 保护 specialLoggers 的互斥锁
	specialLoggersMutex sync.RWMutex
}

// NewZapCoreWithService 创建带有指定服务信息的 ZapCore（优化版本）
func NewZapCoreWithService(level zapcore.Level, svcName string, svcID uint64) *ZapCore {
	// 直接使用传入的服务信息，避免访问全局变量
	entity := &ZapCore{
		level:          level,
		serviceName:    svcName,
		serviceID:      svcID,
		specialLoggers: make(map[string]*lumberjack.Logger),
	}
	syncer := entity.WriteSyncer()

	// 创建并缓存编码器，避免重复创建
	encoder := zapConfig.Encoder()
	entity.encoder = encoder

	// 使用动态级别控制器
	levelEnabler := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
		// 如果当前日志级别小于等于配置的级别，则允许输出
		return l == level && l >= atomicLevel.Level()
	})
	entity.Core = zapcore.NewCore(encoder, syncer, levelEnabler)
	return entity
}

// getLogFileName 根据配置获取日志文件名
// 如果启用了单文件模式，返回配置的单文件名或默认的 "all.log"
// 否则返回基于日志级别的文件名，如 "debug.log"、"info.log" 等
func (z *ZapCore) getLogFileName() string {
	// 如果启用了单文件模式
	if zapConfig.SingleFile {
		// 如果配置了自定义文件名，使用自定义文件名
		if zapConfig.SingleFileName != "" {
			return zapConfig.SingleFileName
		}
		// 否则使用默认文件名
		return "all.log"
	}
	// 按级别分文件模式，使用级别名称作为文件名
	return z.level.String() + ".log"
}

func (z *ZapCore) WriteSyncer(formats ...string) zapcore.WriteSyncer {
	return z.createWriteSyncer(z.serviceName, z.serviceID, formats...)
}

// createWriteSyncer 创建写入同步器，接受服务名称和ID作为参数以避免锁竞争
func (z *ZapCore) createWriteSyncer(currentServiceName string, currentServiceID uint64, formats ...string) zapcore.WriteSyncer {
	// 构建包含服务名称的日志目录路径
	logDir := zapConfig.Director
	if currentServiceID != 0 {
		logDir = filepath.Join(zapConfig.Director, fmt.Sprintf("%d", currentServiceID))
	}
	// 有具体服务的名字要加入到目录中
	if currentServiceName != "" {
		logDir = filepath.Join(logDir, currentServiceName)
	}
	// 如果有额外的格式化目录（如business、folder等），添加到路径中
	if len(formats) > 0 && formats[0] != "" {
		logDir = filepath.Join(logDir, formats[0])
	}
	// 确保目录存在
	if err := os.MkdirAll(logDir, 0755); err != nil {
		// 如果创建目录失败，使用默认目录
		logDir = zapConfig.Director
		os.MkdirAll(logDir, 0755)
	}

	var lumberjackLogger *lumberjack.Logger

	// 获取日志文件名（根据配置决定是单文件还是按级别分文件）
	logFileName := z.getLogFileName()

	// 如果是特殊目录，使用缓存的 logger 避免重复创建和 goroutine 泄露
	if len(formats) > 0 && formats[0] != "" {
		// 构建缓存键：目录路径 + 文件名
		cacheKey := filepath.Join(logDir, logFileName)

		z.specialLoggersMutex.RLock()
		cachedLogger, exists := z.specialLoggers[cacheKey]
		z.specialLoggersMutex.RUnlock()

		if exists {
			// 使用缓存的 logger
			lumberjackLogger = cachedLogger
		} else {
			// 创建新的 logger 并缓存
			lumberjackLogger = &lumberjack.Logger{
				Filename:   filepath.Join(logDir, logFileName),
				MaxSize:    zapConfig.MaxSize,        // MB
				MaxBackups: zapConfig.MaxBackups,     // 保留备份文件数量
				MaxAge:     zapConfig.RetentionDay,   // 保留天数
				Compress:   zapConfig.EnableCompress, // 是否压缩
			}

			// 缓存新创建的 logger
			z.specialLoggersMutex.Lock()
			z.specialLoggers[cacheKey] = lumberjackLogger
			z.specialLoggersMutex.Unlock()
		}
	} else {
		// 主要的 lumberjack logger（非特殊目录）
		lumberjackLogger = &lumberjack.Logger{
			Filename:   filepath.Join(logDir, logFileName),
			MaxSize:    zapConfig.MaxSize,        // MB
			MaxBackups: zapConfig.MaxBackups,     // 保留备份文件数量
			MaxAge:     zapConfig.RetentionDay,   // 保留天数
			Compress:   zapConfig.EnableCompress, // 是否压缩
		}

		// 保存主要的 lumberjack logger 引用，用于后续关闭
		z.lumberjackLogger = lumberjackLogger
	}

	// 同步日志写入 到 控制台
	if zapConfig.LogInConsole {
		multiSyncer := zapcore.NewMultiWriteSyncer(os.Stdout, zapcore.AddSync(lumberjackLogger))
		return multiSyncer
	}
	return zapcore.AddSync(lumberjackLogger)
}

func (z *ZapCore) Enabled(level zapcore.Level) bool {
	// 检查是否与当前核心级别相同，并且大于等于全局设置的级别
	return z.level == level && level >= atomicLevel.Level()
}

func (z *ZapCore) With(fields []zapcore.Field) zapcore.Core {
	return z.Core.With(fields)
}

func (z *ZapCore) Check(entry zapcore.Entry, check *zapcore.CheckedEntry) *zapcore.CheckedEntry {
	// 使用 Enabled 方法检查是否应该记录日志
	if z.Enabled(entry.Level) {
		return check.AddCore(entry, z)
	}
	return check
}

func (z *ZapCore) Write(entry zapcore.Entry, fields []zapcore.Field) error {
	// 创建一个新的 fields 切片，用于存储处理后的字段
	filteredFields := make([]zapcore.Field, 0, len(fields))

	// 检查是否有特殊目录字段，但不修改原始 Core
	var specialDirectory string
	hasSpecialDirectory := false

	for i := 0; i < len(fields); i++ {
		if fields[i].Key == "business" || fields[i].Key == "folder" {
			// business 和 folder 字段总是创建子目录
			specialDirectory = fields[i].String
			hasSpecialDirectory = true
			// 不将此字段添加到 filteredFields 中，实现移除效果
		} else if fields[i].Key == "directory" {
			// directory 字段创建子目录（仅对当前日志生效）
			specialDirectory = fields[i].String
			hasSpecialDirectory = true
			// 不将此字段添加到 filteredFields 中，避免在日志内容中显示
		} else {
			// 保留其他字段
			filteredFields = append(filteredFields, fields[i])
		}
	}

	// 根据是否有特殊目录字段来决定使用哪个 Core
	if hasSpecialDirectory {
		// 创建临时的 Core 用于这次写入，不影响原始 Core
		// 使用缓存的编码器，避免重复创建
		syncer := z.createWriteSyncer(z.serviceName, z.serviceID, specialDirectory)
		tempCore := zapcore.NewCore(z.encoder, syncer, z.level)
		return tempCore.Write(entry, filteredFields)
	} else {
		// 使用原始的 Core（写入主日志目录）
		return z.Core.Write(entry, filteredFields)
	}
}

func (z *ZapCore) Sync() error {
	return z.Core.Sync()
}

// Close 关闭 ZapCore，包括关闭 lumberjack logger 以防止 goroutine 泄露
func (z *ZapCore) Close() error {
	// 先同步日志
	if err := z.Core.Sync(); err != nil {
		// 记录同步错误，但继续关闭流程
		fmt.Fprintf(os.Stderr, "ZapCore 同步失败: %v\n", err)
	}

	// 关闭主要的 lumberjack logger
	if z.lumberjackLogger != nil {
		if err := z.lumberjackLogger.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "关闭主要 lumberjack logger 失败: %v\n", err)
		}
		z.lumberjackLogger = nil
	}

	// 关闭所有缓存的特殊目录 logger
	z.specialLoggersMutex.Lock()
	for cacheKey, logger := range z.specialLoggers {
		if logger != nil {
			if err := logger.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "关闭特殊目录 lumberjack logger 失败 [%s]: %v\n", cacheKey, err)
			}
		}
	}
	// 清空缓存
	z.specialLoggers = make(map[string]*lumberjack.Logger)
	z.specialLoggersMutex.Unlock()

	return nil
}
