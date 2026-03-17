# 代码重构指南

## 已完成的清理工作

### ✅ 阶段 1: 消除重复函数

**创建的文件:**
- `internal/util/helpers.go` - 统一的工具函数库
- `internal/util/helpers_test.go` - 完整的测试覆盖

**消除的重复:**
- `WithDefaultTimeout()` - 从 2 个文件中删除重复
- `UniqueStrings()` - 从 2 个文件中删除重复
- `MinInt()` - 从 2 个文件中删除重复

**修改的文件:** 7 个文件更新为使用统一的工具函数

**验证:** ✅ 所有测试通过，项目编译成功

---

## 待完成的重构任务

### 🔄 阶段 2: 拆分超大文件

#### 优先级 1: `internal/handler/utils.go` (3,362 行, 116 函数)

这个文件严重违反单一职责原则，建议拆分为以下文件：

##### 1. `workdir_extraction.go` (~150 行)
**职责:** 从请求、系统消息、用户消息中提取工作目录

**函数列表:**
```go
// 正则表达式
var explicitEnvWorkdirRegex
var isolatedPrimaryEnvWorkdirRegex
var primaryEnvWorkdirRegex

// 提取函数
func extractWorkdirFromSystem(system []prompt.SystemItem) string
func extractWorkdirFromMessages(messages []prompt.Message) string
func extractWorkdirFromEnvironmentText(text string) string
func extractWorkdirFromRequest(r *http.Request, req ClaudeRequest) (string, string)

// 辅助函数
func looksLikeClaudeEnvironmentBlock(text string) bool
func countNonEmptyLines(text string) int
```

##### 2. `request_helpers.go` (~200 行)
**职责:** HTTP 请求和元数据处理辅助函数

**函数列表:**
```go
func channelFromPath(path string) string
func mapModel(requestModel string) string
func normalizeOrchidsModelKey(model string) string
func conversationKeyForRequest(r *http.Request, req ClaudeRequest) string
func metadataString(metadata map[string]interface{}, keys ...string) string
func headerValue(r *http.Request, keys ...string) string
func extractUserText(messages []prompt.Message) string

// 模型映射表
var orchidsModelMap map[string]string
```

##### 3. `path_utils.go` (~300 行)
**职责:** 路径处理和验证工具

**函数列表:**
```go
func normalizeToolInputPath(path string) string
func toolInputBaseName(path string) string
func looksLikeReadmePath(path string) bool
func looksLikeDependencyManifestPath(path string) bool
func looksLikeCoreImplementationPath(path string) bool
func looksLikeSourceFilePath(path string) bool
func looksLikeNormalToolPath(path string) bool
func isRecoverableMalformedReadPath(path string) bool
func recoverMalformedReadPath(path string) string
func findWindowsToolPathStart(s string) int
```

##### 4. `tool_result_analysis.go` (~800 行)
**职责:** 工具结果分析和证据收集

**函数列表:**
```go
// 类型定义
type toolResultEvidence struct {
    ToolName string
    FilePath string
    Command  string
    Content  string
}

// 分析函数
func lastUserIsToolResultFollowup(messages []prompt.Message) bool
func shouldKeepToolsForWarpToolResultFollowup(messages []prompt.Message) bool
func shouldKeepToolsForRecoverableWarpToolFailure(messages []prompt.Message, original string) bool
func hasSufficientOptimizationEvidence(messages []prompt.Message, allowDeeperExploration bool) bool
func collectSuccessfulToolResultEvidence(messages []prompt.Message) []toolResultEvidence
func countMalformedReadToolUsesInLatestAssistant(messages []prompt.Message) int
func latestToolResultTurnFailureCount(messages []prompt.Message) (total int, failures int)
func buildToolResultFallbackCorpus(messages []prompt.Message) string
func extractToolUseInputString(input interface{}, keys ...string) string
func looksLikeImplementationReadEvidence(item toolResultEvidence) bool
func hasImplementationReadEvidence(messages []prompt.Message) bool
func countImplementationReadEvidence(messages []prompt.Message) int
func countSuccessfulWarpFileToolResults(messages []prompt.Message) int
func extractToolResultContent(content interface{}) string
func lastNonToolResultUserText(messages []prompt.Message) string
func lastToolResultText(messages []prompt.Message) string
func looksLikeToolResultFailure(text string) bool
func looksLikeWarpExplorationSeed(text string) bool
func normalizeToolResultLine(line string) string
func extractEntryFromLongLsLine(line string) (string, bool)
func looksLikeExplorationDirectoryEntry(line string) bool
```

##### 5. `fallback_builders.go` (~1,900 行)
**职责:** 构建各种场景的回退响应

**函数列表:**
```go
func buildToolResultNoToolsFallback(messages []prompt.Message) string
func selectToolResultForFallback(messages []prompt.Message) (string, bool)

// 特定场景的回退构建器 (20+ 个函数)
func buildLocalTechStackFallback(original, toolResult string, preferChinese bool) string
func buildLocalProjectPurposeFallback(toolResult string, preferChinese bool) string
func buildLocalBackendImplementationFallback(toolResult string, preferChinese bool) string
func buildLocalDataLayerFallback(toolResult string, preferChinese bool) string
func buildLocalTestingFallback(toolResult string, preferChinese bool) string
func buildLocalDeploymentFallback(toolResult string, preferChinese bool) string
func buildLocalSecurityRiskFallback(toolResult string, preferChinese bool) string
func buildLocalPermissionRiskFallback(toolResult string, preferChinese bool) string
func buildLocalDependencyRiskFallback(toolResult string, preferChinese bool) string
func buildLocalConfigRiskFallback(toolResult string, preferChinese bool) string
func buildLocalObservabilityGapFallback(toolResult string, preferChinese bool) string
func buildLocalReleaseRiskFallback(toolResult string, preferChinese bool) string
func buildLocalCompatibilityRiskFallback(toolResult string, preferChinese bool) string
func buildLocalOperationalRiskFallback(toolResult string, preferChinese bool) string
func buildLocalRecoveryRollbackRiskFallback(toolResult string, preferChinese bool) string
func buildLocalPerformanceBottleneckFallback(toolResult string, preferChinese bool) string
func buildLocalCodeSmellFallback(toolResult string, preferChinese bool) string
func buildLocalMaintainabilityRiskFallback(toolResult string, preferChinese bool) string
func buildLocalOptimizationFallback(toolResult string, preferChinese bool) string
func buildProjectSpecificOptimizationFallback(messages []prompt.Message, preferChinese bool) string
func buildLocalWebImplementationFallback(toolResult string, preferChinese bool) string

// 辅助函数
func looksLikeOptimizationRequest(text string) bool
func looksLikeTechStackRequest(text string) bool
func looksLikeProjectPurposeRequest(text string) bool
// ... 等等 (20+ 个 looksLike* 函数)
```

##### 6. `suggestion_mode.go` (~200 行)
**职责:** 建议模式相关逻辑

**函数列表:**
```go
func isSuggestionMode(messages []prompt.Message) bool
func buildLocalSuggestion(messages []prompt.Message) string
func containsSuggestionMode(text string) bool
func lastNonSuggestionUserText(messages []prompt.Message) string
func buildToolGateMessage(messages []prompt.Message, suggestionMode bool) string
```

##### 7. `tech_stack_detection.go` (~300 行)
**职责:** 技术栈检测和信号分析

**函数列表:**
```go
type techStackSignals struct {
    hasPython      bool
    hasJavaScript  bool
    hasTypeScript  bool
    hasGo          bool
    hasRust        bool
    hasJava        bool
    // ... 等等
}

func inspectTechStackSignals(corpus string) techStackSignals
func (s techStackSignals) isEmpty() bool
func containsHan(text string) bool
```

---

#### 优先级 2: `internal/handler/stream_handler.go` (3,672 行, 137 函数)

建议拆分为：

##### 1. `stream_writer.go` (~800 行)
**职责:** SSE 流写入逻辑

**函数列表:**
```go
// SSE 常量和变量
const (
    sseEventPrefix = "event: "
    sseDataPrefix  = "data: "
    // ... 等等
)

var (
    sseTextDeltaMarker  []byte
    sseDoneLineBytes    []byte
    // ... 等等
)

// 写入函数
func writeSSEFrame(w io.Writer, event string, data interface{}) error
func writeSSEFrameBytes(w io.Writer, event string, data []byte) error
func flushSSEBuffer(w http.ResponseWriter, buf *bytes.Buffer) error
// ... 等等
```

##### 2. `stream_parser.go` (~900 行)
**职责:** 流数据解析

**函数列表:**
```go
func parseSSEEvent(line []byte) (event string, data []byte, ok bool)
func parseOrchidsStreamChunk(data []byte) (interface{}, error)
func extractTextDelta(data []byte) (string, bool)
// ... 等等
```

##### 3. `tool_use_handler.go` (~1,000 行)
**职责:** 工具使用处理

**函数列表:**
```go
type directToolUseState struct {
    id    string
    name  string
    input *strings.Builder
}

func handleToolUseBlock(block interface{}) error
func extractToolInput(input interface{}) (string, error)
func validateToolUse(toolName string, input interface{}) error
// ... 等等
```

##### 4. `message_builder.go` (~900 行)
**职责:** 消息构建和转换

**函数列表:**
```go
func buildStreamResponse(chunks []interface{}) (interface{}, error)
func convertToOpenAIFormat(claudeMsg interface{}) (interface{}, error)
func buildErrorResponse(err error) interface{}
// ... 等等
```

---

### 🔄 阶段 3: 简化过度抽象

#### 问题: 大量只使用一次的 `looksLike*` 函数

在 `internal/handler/utils.go` 中有 20+ 个 `looksLike*` 函数，大多数只被调用一次。

**建议:**
1. **内联简单的检查** - 如果逻辑只有 2-3 行，直接内联到调用处
2. **合并相似的检查** - 将多个相似的检查合并为一个参数化的函数
3. **保留复杂的检查** - 只保留那些逻辑复杂或被多次调用的函数

**示例:**

```go
// 不好: 只使用一次的简单函数
func looksLikeReadmePath(path string) bool {
    base := toolInputBaseName(path)
    return strings.HasPrefix(base, "readme")
}

// 好: 直接内联
if strings.HasPrefix(toolInputBaseName(path), "readme") {
    // ...
}

// 或者: 合并为参数化函数
func looksLikeSpecialPath(path string, prefixes ...string) bool {
    base := toolInputBaseName(path)
    for _, prefix := range prefixes {
        if strings.HasPrefix(base, prefix) {
            return true
        }
    }
    return false
}
```

---

## 重构步骤

### 步骤 1: 准备工作
1. 确保所有现有测试通过
2. 创建一个新分支用于重构
3. 提交当前状态作为基准

### 步骤 2: 逐个拆分文件
对于每个要拆分的文件：

1. **创建新文件** - 使用建议的文件名
2. **复制函数** - 将相关函数复制到新文件
3. **更新导入** - 确保所有必要的导入都存在
4. **更新原文件** - 从原文件中删除已移动的函数
5. **运行测试** - 确保没有破坏任何功能
6. **提交更改** - 每个拆分作为一个独立的提交

### 步骤 3: 简化抽象
1. **识别候选函数** - 找出只使用一次的函数
2. **评估复杂度** - 决定是内联还是保留
3. **重构** - 逐个处理
4. **测试** - 每次更改后运行测试

### 步骤 4: 验证
1. 运行完整的测试套件
2. 检查代码覆盖率
3. 运行静态分析工具
4. 进行代码审查

---

## 使用的工具

### 静态分析
```bash
# 检测高复杂度函数
go install github.com/fzipp/gocyclo/cmd/gocyclo@latest
gocyclo -over 15 internal/

# 检测代码重复
go install github.com/mibk/dupl@latest
dupl -threshold 50 internal/

# 检测未使用的代码
go install honnef.co/go/tools/cmd/staticcheck@latest
staticcheck ./...
```

### 测试
```bash
# 运行所有测试
go test ./...

# 带覆盖率
go test -cover ./...

# 详细覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

---

## 预期收益

### 代码质量
- ✅ 更好的单一职责原则遵守
- ✅ 更容易理解和维护
- ✅ 更好的测试覆盖率
- ✅ 减少代码重复

### 文件大小
- `utils.go`: 3,362 行 → ~200 行 (保留公共接口)
- `stream_handler.go`: 3,672 行 → ~500 行 (保留主流程)

### 可维护性
- 新功能更容易添加到正确的位置
- Bug 更容易定位和修复
- 代码审查更加高效

---

## 注意事项

1. **保持向后兼容** - 不要改变公共 API
2. **增量重构** - 一次只做一个更改
3. **频繁测试** - 每次更改后都运行测试
4. **文档更新** - 更新相关文档和注释
5. **团队沟通** - 确保团队了解重构计划

---

## 当前状态

- ✅ 阶段 1: 消除重复函数 (已完成)
- 🔄 阶段 2: 拆分超大文件 (进行中 - 已提供指南)
- ⏳ 阶段 3: 简化过度抽象 (待开始)

---

## 下一步行动

1. 审查此重构指南
2. 决定是否继续自动化拆分或手动重构
3. 如果继续，从 `workdir_extraction.go` 开始
4. 每完成一个文件，运行测试并提交

需要我继续自动化拆分第一个文件吗？
