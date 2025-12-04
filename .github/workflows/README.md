# GitHub Actions 自动发布工作流

## 概述

本项目配置了自动发布工作流，当代码推送到主分支（`main` 或 `master`）时，会自动执行打包发布流程。

## 工作流程

1. **代码推送触发**：当代码推送到主分支时自动触发
2. **环境检查**：检查 Go 环境和依赖
3. **测试运行**：自动运行所有测试
4. **代码检查**：执行代码格式化和静态检查
5. **版本计算**：根据 commit 信息自动确定版本号
6. **版本更新**：更新 `version.go` 和 `README.md` 中的版本信息
7. **创建标签**：创建 Git 标签并推送
8. **发布 Release**：在 GitHub 上创建正式发布

## 版本号规则

工作流会根据 commit 信息自动确定版本类型：

### 主版本号递增（Major）
当 commit 信息包含以下内容时：
- `feat!:` 或 `feature!:` - 带有破坏性变更的新功能
- `BREAKING CHANGE:` - 明确标注的破坏性变更
- 包含 `breaking change` 关键词

**示例**：
```bash
git commit -m "feat!: 重构核心 API，不兼容旧版本"
git commit -m "BREAKING CHANGE: 修改日志接口签名"
```

### 次版本号递增（Minor）
当 commit 信息以以下前缀开头时：
- `feat:` 或 `feature:` - 新功能

**示例**：
```bash
git commit -m "feat: 添加异步日志支持"
git commit -m "feature: 新增配置热加载功能"
```

### 补丁版本号递增（Patch）
其他所有类型的 commit：
- `fix:` - Bug 修复
- `docs:` - 文档更新
- `style:` - 代码格式化
- `refactor:` - 代码重构
- `perf:` - 性能优化
- `test:` - 测试相关
- `chore:` - 构建/工具相关

**示例**：
```bash
git commit -m "fix: 修复日志级别判断错误"
git commit -m "docs: 更新 API 文档"
git commit -m "perf: 优化日志写入性能"
```

## 跳过自动发布

如果某次提交不需要触发发布，可以在 commit 信息中添加以下标记：

```bash
git commit -m "docs: 更新文档 [skip release]"
git commit -m "chore: 更新依赖 [no release]"
```

## 首次发布

首次发布时，版本号将根据 commit 类型自动确定：
- 主版本更新 → `v1.0.0`
- 次版本更新 → `v0.1.0`
- 补丁更新 → `v0.0.1`

## 手动发布

如果需要手动控制发布，仍然可以使用原有的 `release.sh` 脚本：

```bash
# 自动递增补丁版本
./release.sh init "修复bug"

# 自动递增次版本
./release.sh minor "新增功能"

# 自动递增主版本
./release.sh major "重大更新"

# 手动指定版本
./release.sh v1.2.3 "自定义版本发布"
```

## 工作流配置

### 触发条件
- 推送到 `main` 或 `master` 分支
- 忽略以下文件的变更：
  - Markdown 文件（`**.md`）
  - `.gitignore`
  - `LICENSE`

### 权限要求
工作流需要 `contents: write` 权限来创建标签和发布。

### 环境要求
- Ubuntu 最新版本
- Go 版本从 `go.mod` 文件自动读取

## 发布产物

每次成功发布后，会生成：

1. **Git 标签**：格式为 `vX.Y.Z`
2. **GitHub Release**：包含变更日志和提交记录
3. **更新的版本文件**：
   - `version.go`（如果存在）
   - `README.md`（如果包含 `go get` 命令）

## 最佳实践

### 1. 使用语义化提交信息
遵循 [Conventional Commits](https://www.conventionalcommits.org/) 规范：

```
<type>[optional scope]: <description>

[optional body]

[optional footer(s)]
```

### 2. 合理使用版本类型
- **Patch（补丁）**：向后兼容的 bug 修复
- **Minor（次版本）**：向后兼容的新功能
- **Major（主版本）**：不兼容的 API 变更

### 3. 编写清晰的提交信息
提交信息会被包含在发布说明中，应该：
- 简洁明了
- 描述变更内容
- 说明变更原因（如果必要）

### 4. 确保测试通过
工作流会自动运行测试，确保：
- 所有测试用例通过
- 代码格式符合规范
- 静态检查无错误

## 故障排查

### 发布失败
如果自动发布失败，检查：
1. GitHub Actions 日志中的错误信息
2. 测试是否全部通过
3. 代码格式是否规范
4. 是否有权限问题

### 版本冲突
如果出现版本号冲突：
1. 检查是否有未推送的标签
2. 手动删除冲突的标签：`git tag -d vX.Y.Z`
3. 重新推送代码触发发布

### 跳过发布未生效
确保在 commit 信息中正确添加了跳过标记：
- `[skip release]`
- `[no release]`
- `[skip-release]`
- `[no-release]`

## 示例工作流

### 场景 1：修复 Bug
```bash
# 修复代码
git add .
git commit -m "fix: 修复日志文件锁定问题"
git push origin main

# 自动触发发布，版本号从 v1.2.3 → v1.2.4
```

### 场景 2：添加新功能
```bash
# 开发新功能
git add .
git commit -m "feat: 添加日志轮转功能"
git push origin main

# 自动触发发布，版本号从 v1.2.4 → v1.3.0
```

### 场景 3：重大更新
```bash
# 重构 API
git add .
git commit -m "feat!: 重构日志接口，简化使用方式

BREAKING CHANGE: Logger.Write() 方法签名已更改"
git push origin main

# 自动触发发布，版本号从 v1.3.0 → v2.0.0
```

### 场景 4：仅更新文档
```bash
# 更新文档
git add .
git commit -m "docs: 更新 API 使用示例 [skip release]"
git push origin main

# 不会触发发布
```

## 相关链接

- [GitHub Actions 文档](https://docs.github.com/en/actions)
- [语义化版本规范](https://semver.org/lang/zh-CN/)
- [Conventional Commits](https://www.conventionalcommits.org/)