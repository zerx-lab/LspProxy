// Package glossary 提供社区可维护的专业术语词汇本功能。
//
// # 目录结构
//
//	<glossaryDir>/
//	    _global.toml          全局词汇本，对所有 LSP 生效
//	    rust-analyzer.toml    rust-analyzer 专属词汇本
//	    clangd.toml           clangd 专属词汇本
//	    pyright.toml          pyright 专属词汇本
//	    ...（文件名 = LSP 可执行文件名，不含扩展名）
//
// # TOML 格式示例
//
//	# rust-analyzer 专属术语
//	[terms]
//	"borrow checker" = "借用检查器"
//	"lifetime"       = "生命周期"
//	"trait bound"    = "trait 约束"
//
// # 查询优先级
//
// LSP 专属词汇本 > 全局词汇本 > 缓存 > 在线翻译
//
// 匹配方式为大小写敏感的**精确匹配**，仅对整段待翻译文本有效，
// 不做子串替换（避免污染翻译引擎的输入）。
package glossary

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"github.com/pelletier/go-toml/v2"

	"github.com/zerx-lab/LspProxy/internal/glossary/builtin"
)

// ─────────────────────────────────────────────
// 常量 / 文件名约定
// ─────────────────────────────────────────────

const (
	// globalFileName 全局词汇本文件名（不含目录）
	globalFileName = "_global.toml"

	// exampleComment 写入空词汇本时的示例注释，帮助社区贡献者快速上手
	exampleComment = `# LspProxy 专业术语词汇本
# 格式说明：
#   [terms] 节下每行为一条术语映射
#   key   = 原文（英文），区分大小写，精确匹配整段文本
#   value = 译文（目标语言）
#
# 示例：
# [terms]
# "borrow checker" = "借用检查器"
# "lifetime"       = "生命周期"
# "trait bound"    = "trait 约束"

[terms]
`
)

// ─────────────────────────────────────────────
// 数据结构
// ─────────────────────────────────────────────

// tomlFile 是 TOML 词汇本的反序列化结构
type tomlFile struct {
	Terms map[string]string `toml:"terms"`
}

// termMap 是术语查询表：key = TrimSpace(原文)，大小写敏感
type termMap map[string]string

// Glossary 管理全局词汇本和各 LSP 专属词汇本，支持热重载。
// 所有公开方法均为线程安全。
type Glossary struct {
	dir    string
	logger *slog.Logger

	mu      sync.RWMutex
	global  termMap            // 全局词汇本
	lspMaps map[string]termMap // lspName → 专属词汇本

	watcher *fsnotify.Watcher
	done    chan struct{}
}

// ─────────────────────────────────────────────
// 构造
// ─────────────────────────────────────────────

// New 创建并初始化词汇本管理器。
//
//   - dir：词汇本目录路径（不存在时自动创建）
//   - lspNames：需要加载的 LSP 名称列表（如 ["rust-analyzer", "clangd"]）；
//     传空列表时仅加载全局词汇本
//
// 创建不会返回错误——目录/文件操作失败时降级为空词汇本，热重载功能按需开启。
func New(dir string, lspNames []string, logger *slog.Logger) *Glossary {
	g := &Glossary{
		dir:     dir,
		logger:  logger,
		global:  make(termMap),
		lspMaps: make(map[string]termMap),
		done:    make(chan struct{}),
	}

	// ── 1. 确保目录存在，写入示例全局词汇本 ──
	if err := g.ensureDir(); err != nil {
		logger.Warn("词汇本目录初始化失败，词汇本功能降级为空",
			slog.String("dir", dir),
			slog.String("error", err.Error()),
		)
		return g
	}

	// ── 2. 加载全局词汇本 ──
	g.reloadGlobal()

	// ── 3. 加载各 LSP 专属词汇本 ──
	for _, name := range lspNames {
		g.reloadLSP(name)
	}

	// ── 4. 启动热重载监听 ──
	if err := g.startWatcher(); err != nil {
		logger.Warn("词汇本热重载监听启动失败",
			slog.String("dir", dir),
			slog.String("error", err.Error()),
		)
	}

	return g
}

// ─────────────────────────────────────────────
// 公开查询接口
// ─────────────────────────────────────────────

// Lookup 在词汇本中查询 text 的译文。
//
// 查询顺序：LSP 专属词汇本（lspName）→ 全局词汇本。
// lspName 为空时仅查全局词汇本。
// 匹配大小写敏感，仅支持整段文本精确匹配。
//
// 命中返回 (译文, true)；未命中返回 ("", false)。
func (g *Glossary) Lookup(text, lspName string) (string, bool) {
	key := strings.TrimSpace(text)
	if key == "" {
		return "", false
	}

	g.mu.RLock()
	defer g.mu.RUnlock()

	// LSP 专属词汇本优先
	if lspName != "" {
		if m, ok := g.lspMaps[lspName]; ok {
			if v, ok := m[key]; ok {
				return v, true
			}
		}
	}

	// 全局词汇本兜底
	if v, ok := g.global[key]; ok {
		return v, true
	}

	return "", false
}

// LSPNames 返回当前已加载专属词汇本的 LSP 名称列表（用于调试/日志）。
func (g *Glossary) LSPNames() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	names := make([]string, 0, len(g.lspMaps))
	for k := range g.lspMaps {
		names = append(names, k)
	}
	return names
}

// EnsureLSP 确保指定 LSP 的专属词汇本已加载。
// 若尚未加载则尝试从磁盘读取（文件不存在时写入空示例文件）。
// 此方法在代理启动后首次收到 LSP 命令时调用，是懒加载入口。
func (g *Glossary) EnsureLSP(lspName string) {
	if lspName == "" {
		return
	}

	g.mu.RLock()
	_, loaded := g.lspMaps[lspName]
	g.mu.RUnlock()

	if !loaded {
		g.reloadLSP(lspName)
	}
}

// Close 停止热重载监听并释放资源。
func (g *Glossary) Close() error {
	select {
	case <-g.done:
	default:
		close(g.done)
	}
	if g.watcher != nil {
		return g.watcher.Close()
	}
	return nil
}

// GlossaryDir 返回词汇本目录路径。
func (g *Glossary) GlossaryDir() string {
	return g.dir
}

// ─────────────────────────────────────────────
// TUI 数据查询接口
// ─────────────────────────────────────────────

// FileInfo 描述一个词汇本文件的基本信息（用于 TUI 展示）。
type FileInfo struct {
	Name      string // 文件名（如 _global.toml、rust-analyzer.toml）
	Path      string // 完整路径
	TermCount int    // 术语条目数
	FileSize  int64  // 文件大小（字节），-1 表示无法读取
}

// TermEntry 描述一个术语条目（用于 TUI 展示）。
type TermEntry struct {
	Key   string // 原文
	Value string // 译文
}

// ListFiles 列出词汇本目录下所有 .toml 文件及其统计信息。
// 结果按文件名排序，_global.toml 始终排在第一位。
func (g *Glossary) ListFiles() []FileInfo {
	entries, err := os.ReadDir(g.dir)
	if err != nil {
		return nil
	}

	var files []FileInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".toml") {
			continue
		}

		path := filepath.Join(g.dir, entry.Name())
		info := FileInfo{
			Name:     entry.Name(),
			Path:     path,
			FileSize: -1,
		}

		if fi, err := os.Stat(path); err == nil {
			info.FileSize = fi.Size()
		}

		// 读取术语条目数
		if m, err := g.loadFile(path); err == nil {
			info.TermCount = len(m)
		}

		files = append(files, info)
	}

	// 排序：_global.toml 置顶，其余按文件名字母序
	sort.Slice(files, func(i, j int) bool {
		if files[i].Name == globalFileName {
			return true
		}
		if files[j].Name == globalFileName {
			return false
		}
		return files[i].Name < files[j].Name
	})

	return files
}

// LoadTerms 读取指定词汇本文件的所有术语条目。
// 返回按 key 字母序排列的条目列表。
func (g *Glossary) LoadTerms(filePath string) ([]TermEntry, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取词汇本失败 [%s]: %w", filePath, err)
	}

	var f tomlFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析 TOML 失败 [%s]: %w", filePath, err)
	}

	entries := make([]TermEntry, 0, len(f.Terms))
	for k, v := range f.Terms {
		entries = append(entries, TermEntry{Key: k, Value: v})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Key < entries[j].Key
	})

	return entries, nil
}

// ─────────────────────────────────────────────
// 内部：加载 / 重载
// ─────────────────────────────────────────────

// ensureDir 确保词汇本目录存在，释放内嵌的默认词汇本，并在全局词汇本不存在时写入示例文件。
//
// 内嵌资源释放策略：已存在的文件不会被覆盖，保护用户编辑的内容。
func (g *Glossary) ensureDir() error {
	if err := os.MkdirAll(g.dir, 0o755); err != nil {
		return fmt.Errorf("创建词汇本目录失败 [%s]: %w", g.dir, err)
	}

	// ── 增量合并内嵌默认词汇本（新文件直接写入，已有文件仅追加新词条）──
	if added, err := builtin.MergeExtract(g.dir); err != nil {
		g.logger.Warn("部分内嵌词汇本合并失败",
			slog.String("dir", g.dir),
			slog.String("error", err.Error()),
		)
	} else if added > 0 {
		g.logger.Info("内嵌默认词汇本已合并",
			slog.String("dir", g.dir),
			slog.Int("added", added),
		)
	}

	// ── 确保全局词汇本存在（内嵌资源中可能没有 _global.toml）──
	globalPath := filepath.Join(g.dir, globalFileName)
	if _, err := os.Stat(globalPath); os.IsNotExist(err) {
		if err := os.WriteFile(globalPath, []byte(exampleComment), 0o644); err != nil {
			return fmt.Errorf("写入默认全局词汇本失败 [%s]: %w", globalPath, err)
		}
		g.logger.Info("已生成默认全局词汇本", slog.String("path", globalPath))
	}

	return nil
}

// loadFile 从 TOML 文件加载词汇本，返回规范化后的 termMap。
// 文件不存在时返回空 map，不报错。
func (g *Glossary) loadFile(path string) (termMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return make(termMap), nil
		}
		return nil, fmt.Errorf("读取词汇本文件失败 [%s]: %w", path, err)
	}

	var f tomlFile
	if err := toml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("解析词汇本 TOML 失败 [%s]: %w", path, err)
	}

	m := make(termMap, len(f.Terms))
	for k, v := range f.Terms {
		// key 规范化：仅 TrimSpace，保持大小写敏感
		m[strings.TrimSpace(k)] = v
	}
	return m, nil
}

// reloadGlobal 重新加载全局词汇本（加写锁）。
func (g *Glossary) reloadGlobal() {
	path := filepath.Join(g.dir, globalFileName)
	m, err := g.loadFile(path)
	if err != nil {
		g.logger.Warn("全局词汇本加载失败，保留原词汇本",
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
		return
	}

	g.mu.Lock()
	g.global = m
	g.mu.Unlock()

	g.logger.Info("全局词汇本已加载",
		slog.String("path", path),
		slog.Int("terms", len(m)),
	)
}

// reloadLSP 重新加载指定 LSP 的专属词汇本（加写锁）。
// 若文件不存在，写入空示例文件后加载空 map。
func (g *Glossary) reloadLSP(lspName string) {
	path := filepath.Join(g.dir, lspName+".toml")

	// 文件不存在时写入空示例文件（引导社区贡献）
	if _, err := os.Stat(path); os.IsNotExist(err) {
		content := fmt.Sprintf("# %s 专属术语词汇本\n%s", lspName, exampleComment)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			g.logger.Warn("写入 LSP 专属词汇本示例文件失败",
				slog.String("lsp", lspName),
				slog.String("path", path),
				slog.String("error", err.Error()),
			)
		} else {
			g.logger.Info("已生成 LSP 专属词汇本示例文件",
				slog.String("lsp", lspName),
				slog.String("path", path),
			)
		}
	}

	m, err := g.loadFile(path)
	if err != nil {
		g.logger.Warn("LSP 专属词汇本加载失败，保留原词汇本",
			slog.String("lsp", lspName),
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
		return
	}

	g.mu.Lock()
	g.lspMaps[lspName] = m
	g.mu.Unlock()

	g.logger.Info("LSP 专属词汇本已加载",
		slog.String("lsp", lspName),
		slog.String("path", path),
		slog.Int("terms", len(m)),
	)
}

// ─────────────────────────────────────────────
// 内部：热重载（fsnotify）
// ─────────────────────────────────────────────

// startWatcher 监听词汇本目录，文件变化时自动热重载对应词汇本。
func (g *Glossary) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建词汇本目录监听器失败: %w", err)
	}
	if err := watcher.Add(g.dir); err != nil {
		watcher.Close()
		return fmt.Errorf("添加词汇本目录监听失败 [%s]: %w", g.dir, err)
	}
	g.watcher = watcher
	go g.watchLoop()
	return nil
}

// watchLoop 后台监听循环，TOML 文件变化时触发对应词汇本的热重载。
func (g *Glossary) watchLoop() {
	for {
		select {
		case <-g.done:
			return
		case event, ok := <-g.watcher.Events:
			if !ok {
				return
			}
			// 仅处理 .toml 文件的写入/创建事件
			if !strings.HasSuffix(event.Name, ".toml") {
				continue
			}
			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) {
				continue
			}

			base := filepath.Base(event.Name)
			name := strings.TrimSuffix(base, ".toml")

			g.logger.Info("词汇本文件已变更，正在热重载",
				slog.String("file", base),
				slog.String("op", event.Op.String()),
			)

			if base == globalFileName {
				g.reloadGlobal()
			} else {
				g.reloadLSP(name)
			}

		case err, ok := <-g.watcher.Errors:
			if !ok {
				return
			}
			g.logger.Warn("词汇本目录监听器错误", slog.String("error", err.Error()))
		}
	}
}
