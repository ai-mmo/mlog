package mlog

import (
	"runtime"
	"sync"
	"testing"
	"time"
)

// TestExtremeConcurrentMapAccess 极限并发测试
// 这个测试会创建极端的并发场景来验证安全性
func TestExtremeConcurrentMapAccess(t *testing.T) {
	// 初始化日志系统
	config := ZapConfig{
		Level:           "info",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        false,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 100000,
		AsyncDropOnFull: true,
	}

	InitialZap("test_extreme", 9999, "info", &config)
	defer Close()

	// 创建多个会被疯狂修改的 map
	maps := make([]map[string]interface{}, 10)
	for i := range maps {
		maps[i] = make(map[string]interface{})
	}

	var wg sync.WaitGroup
	stopCh := make(chan struct{})

	// 每个 map 只有一个写入者，避免 concurrent map writes
	for i := 0; i < len(maps); i++ {
		wg.Add(1)
		go func(mapIndex int) {
			defer wg.Done()
			m := maps[mapIndex]
			counter := 0
			for {
				select {
				case <-stopCh:
					return
				default:
					// 疯狂修改这个 map
					// 添加数据
					for k := 0; k < 20; k++ {
						key := string(rune('A'+k))
						m[key] = counter + k
					}
					// 删除数据
					for k := 0; k < 10; k++ {
						delete(m, string(rune('A'+k)))
					}
					// 添加嵌套结构
					m["nested"] = map[string]int{
						"a": counter,
						"b": counter * 2,
						"c": counter * 3,
					}
					// 添加切片
					m["slice"] = []int{counter, counter + 1, counter + 2, counter + 3, counter + 4}
					// 添加更多复杂数据
					m["complex"] = map[string]interface{}{
						"level1": map[string]int{"x": counter, "y": counter * 2},
						"level2": []string{"a", "b", "c"},
					}
					counter++
				}
			}
		}(i)
	}

	// 启动大量的日志记录 goroutine
	numLoggers := runtime.NumCPU() * 4
	for i := 0; i < numLoggers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopCh:
					return
				default:
					// 疯狂记录所有 map
					for j, m := range maps {
						Info("极限测试 logger=%d map_id=%d data=%v", id, j, m)
						// 同时记录多个 map
						if j < len(maps)-1 {
							Info("多map测试 m1=%v m2=%v", m, maps[j+1])
						}
					}
				}
			}
		}(i)
	}

	// 运行测试
	duration := 5 * time.Second
	t.Logf("运行极限并发测试 %v (maps=%d, loggers=%d)", duration, len(maps), numLoggers)

	time.Sleep(duration)
	close(stopCh)
	wg.Wait()

	// 等待异步日志处理完成
	time.Sleep(2 * time.Second)

	t.Log("✅ 极限并发测试通过！系统在极端压力下保持稳定")
}

// TestConcurrentMapWithDifferentTypes 测试不同类型的并发场景
func TestConcurrentMapWithDifferentTypes(t *testing.T) {
	config := ZapConfig{
		Level:           "debug",
		Format:          "console",
		Director:        "./test_logs",
		ShowLine:        true,
		LogInConsole:    false,
		EnableAsync:     true,
		AsyncBufferSize: 50000,
		AsyncDropOnFull: false,
	}

	InitialZap("test_types", 8888, "debug", &config)
	defer Close()

	// 各种类型的共享数据
	type ComplexStruct struct {
		Name    string
		Value   int
		SubMap  map[string]interface{}
		Slice   []int
		Channel chan int
	}

	sharedData := &ComplexStruct{
		Name:    "test",
		Value:   0,
		SubMap:  make(map[string]interface{}),
		Slice:   make([]int, 0),
		Channel: make(chan int, 10),
	}

	var wg sync.WaitGroup
	duration := 2 * time.Second
	start := time.Now()

	// 修改 goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		counter := 0
		for time.Since(start) < duration {
			sharedData.Value = counter
			sharedData.Name = "test-" + string(rune('A'+counter%26))

			// 修改嵌套 map
			for i := 0; i < 10; i++ {
				sharedData.SubMap[string(rune('a'+i))] = counter + i
			}

			// 修改切片
			if counter%10 == 0 {
				sharedData.Slice = make([]int, counter%20)
				for i := range sharedData.Slice {
					sharedData.Slice[i] = i
				}
			}

			counter++
		}
	}()

	// 日志记录 goroutines
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for time.Since(start) < duration {
				// 记录整个结构体
				Debug("结构体数据 id=%d data=%+v", id, sharedData)
				// 记录嵌套的 map
				Info("嵌套map id=%d submap=%v", id, sharedData.SubMap)
				// 记录指针
				Warn("指针数据 id=%d ptr=%p value=%v", id, sharedData, *sharedData)

				time.Sleep(time.Millisecond)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(time.Second)

	t.Log("✅ 不同类型的并发测试通过")
}

// BenchmarkSafeFormatterUnderPressure 压力下的性能基准测试
func BenchmarkSafeFormatterUnderPressure(b *testing.B) {
	// 创建一个会被并发修改的 map
	sharedMap := make(map[string]interface{})

	// 启动一个 goroutine 持续修改 map
	stopCh := make(chan struct{})
	go func() {
		counter := 0
		for {
			select {
			case <-stopCh:
				return
			default:
				for i := 0; i < 100; i++ {
					sharedMap[string(rune('A'+i%26))] = counter
				}
				counter++
			}
		}
	}()
	defer close(stopCh)

	// 等待一下让修改开始
	time.Sleep(10 * time.Millisecond)

	// 测试安全格式化的性能
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = SafeFormat("压力测试 map=%v", sharedMap)
		}
	})
}