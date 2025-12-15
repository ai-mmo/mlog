package mlog

import (
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	globalMutex sync.RWMutex
	coreMutex   sync.RWMutex
	zapCores    []*ZapCore
	zapLogger   *zap.Logger
)

func initZap(serviceName string, serviceID uint64) (logger *zap.Logger) {
	// 判断是否有Director文件夹
	fi, err := os.Stat(zapConfig.Director)
	if (err == nil && !fi.IsDir()) || os.IsNotExist(err) {
		fmt.Printf("create %v directory\n", zapConfig.Director)
		if err := os.MkdirAll(zapConfig.Director, os.ModePerm); err != nil {
			panic(fmt.Sprintf("创建日志目录失败: %v\n", err))
		}
	}
	// 清空之前的核心
	coreMutex.Lock()
	zapCores = make([]*ZapCore, 0)

	levels := zapConfig.Levels()
	cores := make([]zapcore.Core, 0)

	if zapConfig.SingleFile {
		// 【修复】单文件模式：只创建一个Debug级别的Core
		// 这个Core会处理所有 >= Debug 且 >= atomicLevel 的日志
		// 避免多个Core重复写入同一个文件
		core := NewZapCoreWithService(zapcore.DebugLevel, serviceName, serviceID)
		zapCores = append(zapCores, core)
		cores = append(cores, core)
	} else {
		// 多文件模式：为每个级别创建独立的Core
		// 每个Core只处理自己级别的日志，写入对应的文件
		for i := 0; i < len(levels); i++ {
			core := NewZapCoreWithService(levels[i], serviceName, serviceID)
			zapCores = append(zapCores, core)
			cores = append(cores, core)
		}
	}
	coreMutex.Unlock()

	logger = zap.New(zapcore.NewTee(cores...))

	if zapConfig.ShowLine {
		// 修复 caller skip 设置：
		// 对于直接使用 zap.Logger 的情况（如 global.GLOG.Info()），使用 AddCallerSkip(0)
		// 这样可以正确显示实际调用日志的代码位置，而不是 Go 运行时的位置
		// 注意：如果通过 mlog 包装函数调用，那些函数内部会有额外的 skip 处理
		logger = logger.WithOptions(zap.AddCaller(), zap.AddCallerSkip(0))
	}
	return logger
}
