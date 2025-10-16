package mlog

import (
	"sync"
	"testing"
	"time"
)

// TestSafeFormatterWithConcurrentMap 测试安全格式化器处理并发 map
func TestSafeFormatterWithConcurrentMap(t *testing.T) {
	formatter := NewSafeFormatter()

	// 创建一个会被并发修改的 map
	sharedMap := make(map[string]int)

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// 启动一个 goroutine 不断修改 map
	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				// 疯狂修改 map
				for i := 0; i < 10; i++ {
					sharedMap[string(rune('A'+i))] = counter
				}
				// 删除一些键
				delete(sharedMap, "A")
				counter++
			}
		}
	}()

	// 启动多个 goroutine 使用安全格式化器
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				// 使用安全格式化器，不应该触发 fatal error
				result := formatter.FormatSafely("Worker %d: map=%v", id, sharedMap)
				if result == "" {
					t.Errorf("格式化返回空字符串")
				}
			}
		}(i)
	}

	// 运行一段时间
	time.Sleep(100 * time.Millisecond)
	close(stopCh)
	wg.Wait()

	t.Log("✅ 安全格式化器成功处理并发 map，没有触发 fatal error")
}

// TestSafeFormatterTypes 测试安全格式化器处理各种类型
func TestSafeFormatterTypes(t *testing.T) {
	formatter := NewSafeFormatter()

	tests := []struct {
		name   string
		format string
		args   []interface{}
	}{
		{
			name:   "基本类型",
			format: "int=%d str=%s bool=%v",
			args:   []interface{}{42, "hello", true},
		},
		{
			name:   "切片和数组",
			format: "slice=%v array=%v",
			args:   []interface{}{[]int{1, 2, 3}, [3]string{"a", "b", "c"}},
		},
		{
			name:   "map类型",
			format: "map=%v",
			args:   []interface{}{map[string]int{"a": 1, "b": 2}},
		},
		{
			name:   "嵌套结构",
			format: "nested=%v",
			args: []interface{}{
				map[string]interface{}{
					"slice": []int{1, 2, 3},
					"map":   map[string]string{"k": "v"},
				},
			},
		},
		{
			name:   "nil值",
			format: "nil_map=%v nil_slice=%v",
			args:   []interface{}{map[string]int(nil), []int(nil)},
		},
		{
			name:   "错误类型",
			format: "error=%v",
			args:   []interface{}{&testError{"test error"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatter.FormatSafely(tt.format, tt.args...)
			if result == "" {
				t.Errorf("格式化返回空字符串")
			}
			t.Logf("格式化结果: %s", result)
		})
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// TestSafeFormatGlobal 测试全局安全格式化函数
func TestSafeFormatGlobal(t *testing.T) {
	// 测试并发场景
	sharedData := map[string]interface{}{
		"counter": 0,
		"status":  "running",
	}

	var wg sync.WaitGroup

	// 修改 goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			sharedData["counter"] = i
			sharedData["status"] = "running"
			time.Sleep(time.Microsecond)
		}
	}()

	// 格式化 goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			result := SafeFormat("Data: %v", sharedData)
			if result == "" {
				t.Errorf("SafeFormat 返回空字符串")
			}
			time.Sleep(time.Microsecond)
		}
	}()

	wg.Wait()
	t.Log("✅ 全局 SafeFormat 函数测试通过")
}

// BenchmarkSafeFormatter 性能基准测试
func BenchmarkSafeFormatter(b *testing.B) {
	formatter := NewSafeFormatter()
	testMap := map[string]int{
		"a": 1, "b": 2, "c": 3, "d": 4, "e": 5,
	}

	b.Run("SafeFormat", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = formatter.FormatSafely("test %v", testMap)
		}
	})

	b.Run("DirectFormat", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_ = formatMessage("test %v", []any{testMap}, false)
		}
	})
}

// TestIntegrationWithAsyncLogger 集成测试：使用安全格式化的异步日志
func TestIntegrationWithAsyncLogger(t *testing.T) {
	// 初始化日志系统
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 10000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_safe_format", 9001, "info", &config)
	defer Close()

	// 创建一个会被疯狂修改的 map
	crazyMap := make(map[string]interface{})

	var wg sync.WaitGroup
	duration := 2 * time.Second
	start := time.Now()

	// 写入 goroutine - 疯狂修改 map
	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := 0
		for time.Since(start) < duration {
			// 添加各种类型的数据
			crazyMap["int"] = counter
			crazyMap["string"] = "value" + string(rune(counter%26+'A'))
			crazyMap["slice"] = []int{counter, counter + 1, counter + 2}
			crazyMap["nested"] = map[string]int{"a": counter, "b": counter * 2}

			// 随机删除一些键
			if counter%10 == 0 {
				delete(crazyMap, "string")
			}
			if counter%20 == 0 {
				delete(crazyMap, "nested")
			}

			counter++
		}
	}()

	// 日志记录 goroutines
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for time.Since(start) < duration {
				// 直接传递正在被修改的 map
				// 安全格式化器应该能够处理这种情况
				Info("【安全格式化测试】 goroutine=%d map=%v", id, crazyMap)
				Debug("调试信息 data=%v", crazyMap)
				Warn("警告信息 content=%v", crazyMap)
				Error("错误信息 details=%v", crazyMap)

				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(time.Second) // 等待异步日志处理完成

	t.Log("✅ 集成测试通过：安全格式化器成功处理极端并发场景")
}

// TestLogSafetyModes 测试不同的安全模式
func TestLogSafetyModes(t *testing.T) {
	// 保存原始模式
	originalMode := GetLogSafetyMode()
	defer SetLogSafetyMode(originalMode)

	tests := []struct {
		name     string
		mode     LogSafetyMode
		isAsync  bool
		expected bool
	}{
		{"默认模式-同步", SafetyModeDefault, false, false},
		{"默认模式-异步", SafetyModeDefault, true, true},
		{"始终安全模式-同步", SafetyModeAlways, false, true},
		{"始终安全模式-异步", SafetyModeAlways, true, true},
		{"从不安全模式-同步", SafetyModeNever, false, false},
		{"从不安全模式-异步", SafetyModeNever, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetLogSafetyMode(tt.mode)
			result := shouldUseSafeFormat(tt.isAsync)
			if result != tt.expected {
				t.Errorf("shouldUseSafeFormat(%v) = %v, want %v", tt.isAsync, result, tt.expected)
			}
		})
	}
}
