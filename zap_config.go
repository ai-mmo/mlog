package mlog

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
)

type ZapConfig struct {
	Level         string `mapstructure:"level" json:"level" yaml:"level"`                            // 级别
	Prefix        string `mapstructure:"prefix" json:"prefix" yaml:"prefix"`                         // 日志前缀
	Format        string `mapstructure:"format" json:"format" yaml:"format"`                         // 输出
	Director      string `mapstructure:"director" json:"director"  yaml:"director"`                  // 日志文件夹
	EncodeLevel   string `mapstructure:"encode-level" json:"encode-level" yaml:"encode-level"`       // 编码级
	StacktraceKey string `mapstructure:"stacktrace-key" json:"stacktrace-key" yaml:"stacktrace-key"` // 栈名
	ShowLine      bool   `mapstructure:"show-line" json:"show-line" yaml:"show-line"`                // 显示行
	LogInConsole  bool   `mapstructure:"log-in-console" json:"log-in-console" yaml:"log-in-console"` // 输出控制台
	RetentionDay  int    `mapstructure:"retention-day" json:"retention-day" yaml:"retention-day"`    // 日志保留天数
	// 日志分割配置
	MaxSize        int  `mapstructure:"max-size" json:"max-size" yaml:"max-size"`                      // 日志文件最大大小（MB）
	MaxBackups     int  `mapstructure:"max-backups" json:"max-backups" yaml:"max-backups"`             // 日志文件数量
	EnableSplit    bool `mapstructure:"enable-split" json:"enable-split" yaml:"enable-split"`          // 启用日志分片
	EnableCompress bool `mapstructure:"enable-compress" json:"enable-compress" yaml:"enable-compress"` // 启用日志压缩

	// 异步日志配置
	EnableAsync     bool `mapstructure:"enable-async" json:"enable-async" yaml:"enable-async"`                   // 启用异步日志
	AsyncBufferSize int  `mapstructure:"async-buffer-size" json:"async-buffer-size" yaml:"async-buffer-size"`    // 异步日志缓冲区大小
	AsyncDropOnFull bool `mapstructure:"async-drop-on-full" json:"async-drop-on-full" yaml:"async-drop-on-full"` // 缓冲区满时是否丢弃日志

	// 路径显示配置
	UseRelativePath bool   `mapstructure:"use-relative-path" json:"use-relative-path" yaml:"use-relative-path"` // 使用相对路径显示（默认false 使用绝对路径）
	BuildRootPath   string `mapstructure:"build-root-path" json:"build-root-path" yaml:"build-root-path"`       // 编译根目录路径，用于更准确的相对路径计算
}

// Levels
// 初始化所有的日志级别 上层控制日志级别动态写入
func (c *ZapConfig) Levels() []zapcore.Level {
	levels := make([]zapcore.Level, 0, 7)
	level := zapcore.DebugLevel
	for ; level <= zapcore.FatalLevel; level++ {
		levels = append(levels, level)
	}
	return levels
}

func (c *ZapConfig) Encoder() zapcore.Encoder {
	config := zapcore.EncoderConfig{
		TimeKey:       "time",
		NameKey:       "name",
		LevelKey:      "level",
		CallerKey:     "caller",
		MessageKey:    "message",
		StacktraceKey: c.StacktraceKey,
		LineEnding:    zapcore.DefaultLineEnding,
		EncodeTime: func(t time.Time, encoder zapcore.PrimitiveArrayEncoder) {
			encoder.AppendString(c.Prefix + t.Format("2006-01-02 15:04:05.000"))
		},
		EncodeLevel:    c.LevelEncoder(),
		EncodeCaller:   c.CallerEncoder(),
		EncodeDuration: zapcore.SecondsDurationEncoder,
	}
	if c.Format == "json" {
		return zapcore.NewJSONEncoder(config)
	}
	return zapcore.NewConsoleEncoder(config)

}

// LevelEncoder 根据 EncodeLevel 返回 zapcore.LevelEncoder
func (c *ZapConfig) LevelEncoder() zapcore.LevelEncoder {
	switch {
	case c.EncodeLevel == "LowercaseLevelEncoder": // 小写编码器(默认)
		return zapcore.LowercaseLevelEncoder
	case c.EncodeLevel == "LowercaseColorLevelEncoder": // 小写编码器带颜色
		return zapcore.LowercaseColorLevelEncoder
	case c.EncodeLevel == "CapitalLevelEncoder": // 大写编码器
		return zapcore.CapitalLevelEncoder
	case c.EncodeLevel == "CapitalColorLevelEncoder": // 大写编码器带颜色
		return zapcore.CapitalColorLevelEncoder
	default:
		return zapcore.LowercaseLevelEncoder
	}
}

// CallerEncoder 根据 UseRelativePath 配置返回相应的 CallerEncoder
func (c *ZapConfig) CallerEncoder() zapcore.CallerEncoder {
	if c.UseRelativePath {
		return RelativeCallerEncoder
	}
	return zapcore.FullCallerEncoder
}

// RelativeCallerEncoder 自定义的相对路径编码器
func RelativeCallerEncoder(caller zapcore.EntryCaller, enc zapcore.PrimitiveArrayEncoder) {
	if !caller.Defined {
		enc.AppendString("undefined")
		return
	}

	// 获取相对路径
	relativePath := getRelativePath(caller.File)
	enc.AppendString(relativePath + ":" + strconv.Itoa(caller.Line))
}

// getRelativePath 将绝对路径转换为相对路径（优化版本）
func getRelativePath(absolutePath string) string {
	// 如果缓存可用，优先使用缓存
	if globalPathCache != nil {
		return globalPathCache.getRelativePathCached(absolutePath)
	}

	// 回退到原始实现
	return getRelativePathLegacy(absolutePath)
}

// getRelativePathLegacy 原始实现（向后兼容）
func getRelativePathLegacy(absolutePath string) string {
	// 优先使用配置的编译根目录
	if zapConfig.BuildRootPath != "" {
		if relPath := getRelativePathFromBuildRoot(absolutePath, zapConfig.BuildRootPath); relPath != "" {
			return relPath
		}
	}

	// 回退到使用工作目录
	if workingDir == "" {
		return extractRelativeFromPath(absolutePath)
	}

	if !strings.Contains(absolutePath, workingDir) {
		return absolutePath
	}

	if relPath, err := filepath.Rel(workingDir, absolutePath); err == nil {
		if strings.HasPrefix(relPath, "../") {
			return absolutePath
		}
		return relPath
	}

	return extractRelativeFromPath(absolutePath)
}

// getRelativePathFromBuildRoot 基于编译根目录计算相对路径
func getRelativePathFromBuildRoot(absolutePath, buildRootPath string) string {
	// 清理路径，确保一致性
	cleanAbsPath := filepath.Clean(absolutePath)
	cleanBuildRoot := filepath.Clean(buildRootPath)

	// 检查文件是否在编译根目录内
	if !strings.HasPrefix(cleanAbsPath, cleanBuildRoot) {
		return ""
	}

	// 计算相对路径
	if relPath, err := filepath.Rel(cleanBuildRoot, cleanAbsPath); err == nil {
		// 确保不是以 "../" 开头的路径
		if !strings.HasPrefix(relPath, "../") && relPath != "." {
			return relPath
		}
	}

	return ""
}

// extractRelativeFromPath 从绝对路径中提取相对路径部分（原始实现，保持兼容性）
func extractRelativeFromPath(absolutePath string) string {
	// 查找项目根目录标识（如 "aimmo" 或其他项目名）
	parts := strings.Split(absolutePath, string(filepath.Separator))

	// 寻找项目根目录
	for i, part := range parts {
		if part == "aimmo" || part == "plugin" {
			// 从项目根目录开始构建相对路径
			if i < len(parts) {
				return strings.Join(parts[i:], string(filepath.Separator))
			}
		}
	}

	// 如果找不到项目根目录，返回文件名和上级目录
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], string(filepath.Separator))
	}

	return filepath.Base(absolutePath)
}
