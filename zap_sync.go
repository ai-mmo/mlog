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
	length := len(levels)
	cores := make([]zapcore.Core, 0, length)

	for i := 0; i < length; i++ {
		core := NewZapCoreWithService(levels[i], serviceName, serviceID)
		zapCores = append(zapCores, core)
		cores = append(cores, core)
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
