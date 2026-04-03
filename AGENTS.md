# AGENTS.md — LspProxy

本文件为 AI 编码代理（如 Claude、Copilot、Cursor 等）提供在此仓库中工作的指南。

## 项目概述

`LspProxy` 是一个 LSP 中文翻译代理。以透明代理方式插入编辑器（VSCode/Neovim/Zed）与真实 LSP
进程之间，将 LSP 消息（hover、completion、diagnostics、signatureHelp）中的英文文档实时翻译为中文。

```
编辑器 stdin  ──► [forwardClientToLsp goroutine] ──► lsp stdin
编辑器 stdout ◄── [forwardLspToClient goroutine] ◄── lsp stdout
                         ↑ 翻译处理发生在此处
```

---

## 构建 / 运行命令

```bash
# 构建二进制
go build -o LspProxy .

# 安装到 $GOPATH/bin
go install .

# 代理模式（最常用）
./LspProxy -- rust-analyzer

# TUI 管理界面
./LspProxy --tui

# 指定翻译引擎
./LspProxy -e openai -- clangd

# 指定配置文件
./LspProxy --config /path/to/config.yaml -- rust-analyzer
```

## Lint / Format 命令

```bash
# 格式化（强制标准，提交前必须执行）
go fmt ./...

# 静态分析
go vet ./...

# 可选：golangci-lint（需单独安装）
golangci-lint run

# 可选：staticcheck
staticcheck ./...
```

## 测试命令

项目目前没有测试文件（`_test.go`）。添加测试时遵循以下约定：

```bash
# 运行所有测试
go test ./...

# 运行指定包
go test ./internal/markdown/...
go test ./internal/translate/...
go test ./internal/lsp/...

# 运行单个测试函数（-run 接受正则表达式）
go test ./internal/markdown/... -run TestSplit
go test ./internal/translate/... -run TestCachedEngine

# 详细输出
go test ./... -v

# 竞态检测（并发代码必须通过）
go test ./... -race

# 覆盖率报告
go test ./... -cover
```

最适合优先补充测试的模块（纯函数，无副作用）：
- `internal/markdown/splitter.go` — `Split()` 是无副作用纯函数
- `internal/translate/engine.go` — `CachedEngine` LRU 逻辑
- `internal/lsp/message.go` — `IsRequest()` / `IsNotification()` / `IsResponse()` 判断

## 网站开发（website/）

```bash
cd website

bun install       # 安装依赖（使用 Bun，不要用 npm/yarn）
bun run build     # 生产构建
```

---

## 项目结构

```
LspProxy/
├── main.go                  # 入口：调用 cmd.Execute()
├── go.mod                   # 模块名 LspProxy，go 1.26.1
├── cmd/
│   ├── root.go              # 根命令，--config 全局标志
│   └── run.go               # 代理模式 & TUI 模式、日志初始化
├── internal/
│   ├── config/config.go     # 配置结构体、YAML 加载/保存（viper）
│   ├── lsp/
│   │   ├── jsonrpc.go       # LSP Content-Length 帧读写
│   │   ├── message.go       # JSON-RPC 消息类型定义
│   │   └── handler.go       # 消息处理器（翻译调度、缓存快速路径）
│   ├── markdown/splitter.go # Markdown 分割：Text vs Code 片段
│   ├── proxy/proxy.go       # 代理核心：子进程管理、双向消息转发
│   └── translate/
│       ├── engine.go        # Engine 接口 + LRU 内存缓存（CachedEngine）
│       ├── new.go           # 工厂函数：创建三级缓存引擎
│       ├── google.go        # Google 免费翻译 API
│       ├── openai.go        # OpenAI 兼容 API（DeepSeek/Qwen/Ollama）
│       └── dict.go          # DiskDict（JSON 词典）+ 三级缓存
├── tui/
│   ├── app.go               # Bubble Tea Model/Update/View
│   └── styles/styles.go     # lipgloss 样式常量
└── website/                 # 文档网站
```

**层级职责（关注点严格分离）：**

| 层 | 职责 |
|---|---|
| `cmd/` | CLI 解析、参数验证、组件装配 |
| `internal/config/` | 配置加载/保存，不依赖其他内部包 |
| `internal/lsp/` | 协议实现，不知道翻译细节 |
| `internal/translate/` | 翻译逻辑，不知道 LSP 协议 |
| `internal/markdown/` | 文本分割，零外部依赖 |
| `internal/proxy/` | 流程编排，组合以上所有组件 |
| `tui/` | 界面展示，只调用 `config` 包 |

---

## 代码风格规范

### 导入组织

遵循 **goimports** 三组分隔惯例，组间必须有空行：

```go
import (
    // 第 1 组：标准库
    "context"
    "encoding/json"
    "fmt"
    "log/slog"

    // 第 2 组：第三方库
    "github.com/charmbracelet/bubbletea"
    "github.com/spf13/cobra"

    // 第 3 组：项目内部包
    "LspProxy/internal/config"
    "LspProxy/internal/proxy"
)
```

### 命名约定

| 类别 | 约定 | 示例 |
|---|---|---|
| 包名 | 小写单词，与目录同名 | `package translate`、`package lsp` |
| 类型/接口 | PascalCase | `CachedEngine`、`BaseMessage`、`Engine` |
| 公开函数/方法 | PascalCase | `NewHandler`、`WriteMessage` |
| 私有函数/方法 | camelCase | `popMethod`、`flushText` |
| 常量（公开） | PascalCase | `KindCode`、`KindText` |
| 常量（私有） | camelCase | `cacheCheckTimeout` |
| 变量 | camelCase | `lspCommand`、`cfgPath` |
| 文件名 | 小写，下划线分隔 | `handler.go`、`splitter.go` |

### 错误处理

**原则：错误包装用 `%w`，降级策略优先于中止，翻译失败必须透传原文。**

```go
// 包装错误，保留调用链
return nil, fmt.Errorf("加载配置失败 [%s]: %w", path, err)

// 降级：初始化失败时使用简化方案，不中断主流程
disk, err := NewDiskDict(dictPath)
if err != nil {
    return NewCachedEngine(base, memoryLimit), nil // 退化为纯内存
}

// 翻译失败：透传原始消息，绝不丢弃 LSP 消息
if err != nil {
    h.logger.Warn("翻译失败，返回原文", slog.String("error", err.Error()))
    return raw, nil
}

// 有意忽略错误时必须加 nolint 注释说明原因
go warmCache(bgCtx) //nolint:errcheck // 仅预热缓存，忽略结果
```

### 类型使用

- 用 `json.RawMessage` 保留多态 JSON 字段，延迟解析（LSP 消息字段类型不固定）
- 用 `iota` 定义有序枚举常量组
- 嵌套匿名结构体仅用于一次性的内部 API 响应解析
- 并发保护用 `sync.Mutex` / `sync.RWMutex`，不用 channel 模拟锁
- 字符串拼接用 `strings.Builder`

### 注释风格

```go
// Package translate 实现翻译引擎接口及三级缓存机制。
// （文件头必须有包级注释）

// ─────────────────────────────────────────────
// 区块分隔符（视觉对齐）

// ── 1. 初始化内存缓存 ────────────────────────
// 步骤式编号注释

// NewCachedEngine 创建带 LRU 内存缓存的翻译引擎包装器。
// memoryLimit 单位为字节，0 表示不限制。
// （公开函数必须有 godoc 注释）
```

- 代码注释统一使用**简体中文**

### 架构关键模式

**快速路径（50ms 超时）+ 后台预热：**
```go
fastCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
defer cancel()
result, err := engine.Translate(fastCtx, text)
if err != nil {
    go engine.Translate(bgCtx, text) //nolint:errcheck // 后台预热
    return rawMsg, nil               // 立即返回原文
}
```

**三级缓存顺序：** 内存 LRU → 磁盘 JSON 词典 → 在线翻译 API

**Markdown 分割原则：** 调用 `markdown.Split()` 后只翻译 `KindText` 片段，`KindCode` 原样保留。

---

## 依赖说明

主要第三方依赖（见 `go.mod`）：

| 包 | 用途 |
|---|---|
| `github.com/spf13/cobra` | CLI 命令框架 |
| `github.com/spf13/viper` | 配置文件加载（YAML） |
| `github.com/charmbracelet/bubbletea` | TUI 框架（MVU 架构） |
| `github.com/charmbracelet/lipgloss` | TUI 样式 |
| `github.com/charmbracelet/bubbles` | TUI 组件（viewport、textinput） |
| `golang.org/x/net` | 网络工具（HTTP 客户端辅助） |
