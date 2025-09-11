package mlog

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/ai-mmo/lumberjack"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ZapCore struct {
	level       zapcore.Level
	serviceName string // 保存创建时的服务名称
	serviceID   uint64 // 保存创建时的服务ID
	zapcore.Core
}

// NewZapCoreWithService 创建带有指定服务信息的 ZapCore（优化版本）
func NewZapCoreWithService(level zapcore.Level, svcName string, svcID uint64) *ZapCore {
	// 直接使用传入的服务信息，避免访问全局变量
	entity := &ZapCore{
		level:       level,
		serviceName: svcName,
		serviceID:   svcID,
	}
	syncer := entity.WriteSyncer()
	// 使用动态级别控制器
	levelEnabler := zap.LevelEnablerFunc(func(l zapcore.Level) bool {
		// 如果当前日志级别小于等于配置的级别，则允许输出
		return l == level && l >= atomicLevel.Level()
	})
	entity.Core = zapcore.NewCore(zapConfig.Encoder(), syncer, levelEnabler)
	return entity
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

	// 创建 lumberjack logger
	lumberjackLogger := &lumberjack.Logger{
		Filename:   filepath.Join(logDir, z.level.String()+".log"),
		MaxSize:    zapConfig.MaxSize,        // MB
		MaxBackups: zapConfig.MaxBackups,     // 保留备份文件数量
		MaxAge:     zapConfig.RetentionDay,   // 保留天数
		Compress:   zapConfig.EnableCompress, // 是否压缩
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

	for i := 0; i < len(fields); i++ {
		if fields[i].Key == "business" || fields[i].Key == "folder" || fields[i].Key == "directory" {
			// 使用该 Core 创建时保存的服务信息
			syncer := z.createWriteSyncer(z.serviceName, z.serviceID, fields[i].String)
			z.Core = zapcore.NewCore(zapConfig.Encoder(), syncer, z.level)
			// 不将此字段添加到 filteredFields 中，实现移除效果
		} else {
			// 保留其他字段
			filteredFields = append(filteredFields, fields[i])
		}
	}

	// 使用过滤后的字段调用底层 Core 的 Write 方法
	return z.Core.Write(entry, filteredFields)
}

func (z *ZapCore) Sync() error {
	return z.Core.Sync()
}
