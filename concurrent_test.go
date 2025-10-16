package mlog

import (
	"sync"
	"testing"
	"time"
)

// TestConcurrentMapLogging 测试并发修改 map 时的日志记录
// 这个测试用于验证修复 "concurrent map iteration and map write" 错误
func TestConcurrentMapLogging(t *testing.T) {
	// 初始化日志系统，启用异步日志
	config := ZapConfig{
		Level:           "debug",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    true,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_service", 1001, "debug", &config)
	defer Close()

	// 创建一个共享的 map
	sharedMap := make(map[string]int)
	var mu sync.Mutex

	// 启动多个 goroutine 并发修改 map
	var wg sync.WaitGroup
	numWriters := 10
	numReaders := 10
	duration := 2 * time.Second

	// 写入 goroutines
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start := time.Now()
			counter := 0
			for time.Since(start) < duration {
				mu.Lock()
				sharedMap[string(rune('A'+id))] = counter
				mu.Unlock()
				counter++
				time.Sleep(time.Microsecond * 100)
			}
		}(i)
	}

	// 日志记录 goroutines - 记录包含 map 的日志
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			start := time.Now()
			for time.Since(start) < duration {
				// 创建一个本地 map 的副本用于日志记录
				// 注意：这里故意不加锁，模拟真实场景中可能出现的并发问题
				localMap := make(map[string]int)
				mu.Lock()
				for k, v := range sharedMap {
					localMap[k] = v
				}
				mu.Unlock()

				// 记录包含 map 的日志
				// 在修复前，这里可能会触发 "concurrent map iteration and map write" 错误
				Info("测试并发日志记录 goroutine=%d map=%v", id, localMap)
				time.Sleep(time.Millisecond * 10)
			}
		}(i)
	}

	// 等待所有 goroutine 完成
	wg.Wait()

	// 给异步日志一些时间来处理剩余的日志
	time.Sleep(time.Second)

	t.Log("并发测试完成，没有发生 panic")
}

// TestConcurrentMapLoggingWithLockProtection 测试使用锁保护的并发 map 访问
// 这是推荐的使用方式：在记录日志前使用锁保护共享数据
func TestConcurrentMapLoggingWithLockProtection(t *testing.T) {
	// 初始化日志系统
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false, // 关闭控制台输出以提高性能
		EnableAsync:     true,
		AsyncBufferSize: 50000,
		AsyncDropOnFull: true, // 允许丢弃日志以避免阻塞
	}

	InitialZap("test_service", 1002, "info", &config)
	defer Close()

	// 创建一个共享的 map 和保护它的锁
	sharedMap := make(map[string]int)
	var mapMu sync.RWMutex

	var wg sync.WaitGroup
	duration := 1 * time.Second

	// 写入 goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		counter := 0
		for time.Since(start) < duration {
			mapMu.Lock()
			sharedMap["key"] = counter
			mapMu.Unlock()
			counter++
		}
	}()

	// 日志记录 goroutine - 使用锁保护
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		for time.Since(start) < duration {
			// 【正确做法】使用读锁保护 map 访问
			mapMu.RLock()
			mapCopy := make(map[string]int, len(sharedMap))
			for k, v := range sharedMap {
				mapCopy[k] = v
			}
			mapMu.RUnlock()

			// 使用副本记录日志，避免并发问题
			Info("共享 map 状态: %v", mapCopy)
		}
	}()

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("带锁保护的并发测试完成")
}

// TestAsyncLoggingPerformance 测试异步日志的性能
func TestAsyncLoggingPerformance(t *testing.T) {
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 100000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_service", 1003, "info", &config)
	defer Close()

	// 测试大量日志记录
	numLogs := 10000
	start := time.Now()

	for i := 0; i < numLogs; i++ {
		testMap := map[string]interface{}{
			"index":     i,
			"timestamp": time.Now().Unix(),
			"data":      "test data",
		}
		Info("性能测试日志 index=%d data=%v", i, testMap)
	}

	elapsed := time.Since(start)
	t.Logf("记录 %d 条日志耗时: %v (平均 %.2f μs/条)", numLogs, elapsed, float64(elapsed.Microseconds())/float64(numLogs))

	// 等待异步日志处理完成
	time.Sleep(2 * time.Second)
}

// TestComplexDataStructures 测试复杂数据结构的日志记录
func TestComplexDataStructures(t *testing.T) {
	config := ZapConfig{
		Level:           "debug",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    true,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_service", 1004, "debug", &config)
	defer Close()

	// 测试嵌套的复杂数据结构
	complexData := map[string]interface{}{
		"string": "test",
		"int":    123,
		"float":  45.67,
		"bool":   true,
		"slice":  []int{1, 2, 3, 4, 5},
		"nested_map": map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
		"nil_value": nil,
	}

	var wg sync.WaitGroup
	numGoroutines := 5

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Info("复杂数据结构测试 goroutine=%d iteration=%d data=%v", id, j, complexData)
				Debug("调试信息 goroutine=%d iteration=%d", id, j)
				Warn("警告信息 goroutine=%d data=%v", id, complexData)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("复杂数据结构测试完成")
}

// TestConcurrentMapLoggingWithoutLock 测试不加锁的并发 map 访问（演示问题）
// 警告：这个测试故意不使用锁，用于演示并发问题
// 在没有修复的情况下，这个测试可能会导致 fatal error
func TestConcurrentMapLoggingWithoutLock(t *testing.T) {
	// 跳过这个测试，因为它可能导致 fatal error
	// 如果要运行这个测试，请使用: go test -run TestConcurrentMapLoggingWithoutLock -tags=dangerous
	t.Skip("跳过危险测试，使用 -tags=dangerous 来运行")

	// 初始化日志系统
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        false,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: true,
	}

	InitialZap("test_service", 1005, "info", &config)
	defer Close()

	// 创建一个共享的 map，不使用锁保护
	sharedMap := make(map[string]int)

	var wg sync.WaitGroup
	duration := 500 * time.Millisecond

	// 写入 goroutine - 不断修改 map
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		counter := 0
		for time.Since(start) < duration {
			// 故意不加锁
			for i := 0; i < 10; i++ {
				sharedMap[string(rune('A'+i))] = counter
			}
			counter++
			// 删除一些键，增加并发冲突的概率
			if counter%10 == 0 {
				delete(sharedMap, "A")
			}
		}
	}()

	// 日志记录 goroutine - 直接传递正在被修改的 map
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		for time.Since(start) < duration {
			// 危险：直接传递可能正在被修改的 map
			// 在没有修复的情况下，这可能触发 fatal error
			Info("危险测试: map=%v", sharedMap)
			time.Sleep(time.Microsecond * 100)
		}
	}()

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("危险测试完成（如果能看到这条消息，说明修复有效）")
}