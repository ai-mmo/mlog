package mlog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	lru "github.com/hashicorp/golang-lru/v2"
)

// 全局路径缓存实例
var (
	globalPathCache *PathCache
	workingDir      string // 保持向后兼容
)

func init() {
	// 初始化时获取工作目录
	if wd, err := os.Getwd(); err == nil {
		workingDir = wd
	}
}

// PathCacheEntry 缓存条目结构
type PathCacheEntry struct {
	relativePath  string
	isProjectFile bool
}

// PathCache 路径缓存结构
type PathCache struct {
	cache        *lru.Cache[string, *PathCacheEntry]
	mutex        sync.RWMutex
	workDir      string
	workDirLen   int
	buildRoot    string // 编译根目录
	projectRoots []string
	// 预编译的正则表达式用于堆栈处理
	stackPathRegex *regexp.Regexp
}

// initPathCache 初始化路径缓存
func initPathCache() {
	cache, err := lru.New[string, *PathCacheEntry](1000) // 缓存1000个路径
	if err != nil {
		// 如果创建缓存失败，使用nil缓存（回退到原始实现）
		return
	}

	// 预编译正则表达式用于堆栈路径匹配
	stackRegex, _ := regexp.Compile(`(/[^:\s]+\.go):(\d+)`)

	globalPathCache = &PathCache{
		cache:          cache,
		workDir:        workingDir,
		workDirLen:     len(workingDir),
		buildRoot:      "",                                  // 将在配置加载后设置
		projectRoots:   []string{"aimmo", "plugin", "mlog"}, // 可配置的项目根目录
		stackPathRegex: stackRegex,
	}
}

// updateBuildRoot 更新缓存中的编译根目录
func updateBuildRoot(buildRootPath string) {
	if globalPathCache != nil {
		globalPathCache.mutex.Lock()
		globalPathCache.buildRoot = buildRootPath
		// 清空缓存，因为编译根目录改变了
		globalPathCache.cache.Purge()
		globalPathCache.mutex.Unlock()
	}
}

// getRelativePathCached 使用缓存的路径转换
func (pc *PathCache) getRelativePathCached(absolutePath string) string {
	// 读锁检查缓存
	pc.mutex.RLock()
	if entry, ok := pc.cache.Get(absolutePath); ok {
		pc.mutex.RUnlock()
		return entry.relativePath
	}
	pc.mutex.RUnlock()

	// 缓存未命中，计算相对路径
	relativePath := pc.computeRelativePath(absolutePath)

	// 写锁更新缓存
	pc.mutex.Lock()
	pc.cache.Add(absolutePath, &PathCacheEntry{
		relativePath:  relativePath,
		isProjectFile: pc.isProjectFile(absolutePath),
	})
	pc.mutex.Unlock()

	return relativePath
}

// computeRelativePath 计算相对路径（优化的核心逻辑）
func (pc *PathCache) computeRelativePath(absolutePath string) string {
	// 优先使用编译根目录
	if pc.buildRoot != "" {
		if relPath := pc.getRelativePathFromBuildRootCached(absolutePath); relPath != "" {
			return relPath
		}
	}

	// 回退到使用工作目录
	if pc.workDir == "" {
		return pc.extractRelativeFromPathOptimized(absolutePath)
	}

	// 检查是否是项目内的文件（使用与原始实现相同的逻辑）
	if !strings.Contains(absolutePath, pc.workDir) {
		// 不是项目内文件，保持原路径（与原始实现一致）
		return absolutePath
	}

	// 尝试获取相对路径（使用 filepath.Rel 保持与原始实现一致）
	if relPath, err := filepath.Rel(pc.workDir, absolutePath); err == nil {
		// 如果相对路径以 "../" 开头，说明文件在项目外，保持原路径
		if strings.HasPrefix(relPath, "../") {
			return absolutePath
		}
		return relPath
	}

	// 如果失败，使用备用方法
	return pc.extractRelativeFromPathOptimized(absolutePath)
}

// getRelativePathFromBuildRootCached 基于编译根目录计算相对路径（缓存版本）
func (pc *PathCache) getRelativePathFromBuildRootCached(absolutePath string) string {
	// 清理路径，确保一致性
	cleanAbsPath := filepath.Clean(absolutePath)
	cleanBuildRoot := filepath.Clean(pc.buildRoot)

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

// isProjectFile 判断是否是项目文件
func (pc *PathCache) isProjectFile(absolutePath string) bool {
	for _, root := range pc.projectRoots {
		if strings.Contains(absolutePath, string(filepath.Separator)+root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// extractRelativeFromPathOptimized 优化的路径提取方法
func (pc *PathCache) extractRelativeFromPathOptimized(absolutePath string) string {
	// 使用预定义的项目根目录进行快速匹配
	for _, root := range pc.projectRoots {
		rootPattern := string(filepath.Separator) + root + string(filepath.Separator)
		if idx := strings.Index(absolutePath, rootPattern); idx != -1 {
			// 找到项目根目录，从这里开始构建相对路径
			return absolutePath[idx+1:] // +1 跳过开头的分隔符
		}
	}

	// 如果没有找到项目根目录，使用快速的后缀提取
	return pc.extractLastTwoSegments(absolutePath)
}

// extractLastTwoSegments 快速提取路径的最后两个段
func (pc *PathCache) extractLastTwoSegments(absolutePath string) string {
	// 从后往前查找两个分隔符
	lastSep := strings.LastIndex(absolutePath, string(filepath.Separator))
	if lastSep == -1 {
		return absolutePath
	}

	secondLastSep := strings.LastIndex(absolutePath[:lastSep], string(filepath.Separator))
	if secondLastSep == -1 {
		return absolutePath[lastSep+1:]
	}

	return absolutePath[secondLastSep+1:]
}

// ClearCache 清空路径缓存（用于测试或重置）
func (pc *PathCache) ClearCache() {
	if pc == nil {
		return
	}
	pc.mutex.Lock()
	pc.cache.Purge()
	pc.mutex.Unlock()
}

// GetCacheStats 获取缓存统计信息
func (pc *PathCache) GetCacheStats() (hits, misses int) {
	if pc == nil {
		return 0, 0
	}
	pc.mutex.RLock()
	defer pc.mutex.RUnlock()
	return pc.cache.Len(), 0 // LRU v2 不直接提供 miss 统计
}

// UpdateWorkingDirectory 更新工作目录（用于动态配置）
func (pc *PathCache) UpdateWorkingDirectory(newWorkDir string) {
	if pc == nil {
		return
	}
	pc.mutex.Lock()
	pc.workDir = newWorkDir
	pc.workDirLen = len(newWorkDir)
	// 清空缓存，因为工作目录变了
	pc.cache.Purge()
	pc.mutex.Unlock()
}
