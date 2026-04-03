// Package translate 提供提示词模板的加载与热重载功能。
package translate

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"

	"github.com/fsnotify/fsnotify"
)

// ─────────────────────────────────────────────
// 内置默认提示词模板
// ─────────────────────────────────────────────

// defaultPromptTemplate 是内置的默认系统提示词（text/template 格式）。
// 可用模板变量：
//   - {{.TargetLang}}：目标翻译语言（如 "zh-CN"、"Japanese"）
const defaultPromptTemplate = "You are a professional technical documentation translator. " +
	"Translate the user's input into {{.TargetLang}}. " +
	"Rules you MUST follow:\n" +
	"1. Preserve all Markdown formatting (headings, bold, italic, code blocks, inline code, lists, links, etc.) exactly as-is.\n" +
	"2. Placeholders in the format $CODE_N$ (where N is a number, e.g. $CODE_0$, $CODE_1$) represent code snippets. " +
	"Keep them EXACTLY as-is — do NOT translate, modify, move, or remove them.\n" +
	"3. Output ONLY the translated text. No explanations, no preamble, no commentary."

// ─────────────────────────────────────────────
// PromptData 模板数据
// ─────────────────────────────────────────────

// PromptData 是提示词模板渲染时注入的数据结构。
type PromptData struct {
	// TargetLang 目标翻译语言，例如 "zh-CN"、"Japanese"
	TargetLang string
}

// ─────────────────────────────────────────────
// PromptLoader 结构体
// ─────────────────────────────────────────────

// PromptLoader 从文件加载提示词模板，并通过 fsnotify 监听文件变化实现热重载。
// 所有公开方法均为线程安全。
type PromptLoader struct {
	filePath string
	logger   *slog.Logger

	mu      sync.RWMutex
	tmpl    *template.Template // 当前已编译模板（来自文件或内置）
	builtin *template.Template // 内置默认模板（文件加载/解析失败时的回退）

	watcher *fsnotify.Watcher
	done    chan struct{} // 关闭信号
}

// NewPromptLoader 创建 PromptLoader：
//   - 若提示词文件不存在，自动写入内置默认模板供用户编辑
//   - 加载并编译文件中的模板
//   - 启动 fsnotify 目录监听，文件变化时自动热重载
//
// 创建不会返回错误——若文件操作失败，则回退到内置默认模板；
// 若监听启动失败，则仅禁用热重载功能，翻译功能不受影响。
func NewPromptLoader(filePath string, logger *slog.Logger) *PromptLoader {
	// 编译内置模板（语法固定，解析失败为编程错误）
	builtin := template.Must(template.New("builtin").Parse(defaultPromptTemplate))

	pl := &PromptLoader{
		filePath: filePath,
		logger:   logger,
		builtin:  builtin,
		tmpl:     builtin, // 初始先使用内置，加载成功后替换
		done:     make(chan struct{}),
	}

	// ── 1. 确保文件存在（首次运行时写入默认内容）────────────────────────
	if err := pl.ensureFile(); err != nil {
		logger.Warn("提示词文件初始化失败，使用内置默认模板",
			slog.String("path", filePath),
			slog.String("error", err.Error()),
		)
	} else {
		// ── 2. 从文件加载模板 ────────────────────────────────────────────
		if err := pl.reload(); err != nil {
			logger.Warn("提示词文件加载失败，使用内置默认模板",
				slog.String("path", filePath),
				slog.String("error", err.Error()),
			)
		}
	}

	// ── 3. 启动目录监听（失败时仅禁用热重载，不影响翻译功能）────────────
	if err := pl.startWatcher(); err != nil {
		logger.Warn("提示词热重载监听启动失败",
			slog.String("path", filePath),
			slog.String("error", err.Error()),
		)
	}

	return pl
}

// Render 使用当前提示词模板渲染系统提示词，targetLang 为目标语言。
// 若渲染失败则自动回退到内置默认模板。
func (pl *PromptLoader) Render(targetLang string) string {
	data := PromptData{TargetLang: targetLang}

	pl.mu.RLock()
	tmpl := pl.tmpl
	pl.mu.RUnlock()

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		pl.logger.Warn("提示词模板渲染失败，回退到内置默认模板",
			slog.String("error", err.Error()),
		)
		// 内置模板语法固定，不应失败
		var fallback strings.Builder
		_ = pl.builtin.Execute(&fallback, data)
		return fallback.String()
	}
	return buf.String()
}

// Close 停止文件监听并释放资源。
func (pl *PromptLoader) Close() error {
	select {
	case <-pl.done:
		// 已关闭，幂等操作
	default:
		close(pl.done)
	}
	if pl.watcher != nil {
		return pl.watcher.Close()
	}
	return nil
}

// FilePath 返回提示词文件的磁盘路径。
func (pl *PromptLoader) FilePath() string {
	return pl.filePath
}

// ─────────────────────────────────────────────
// 内部方法
// ─────────────────────────────────────────────

// ensureFile 若提示词文件不存在，则写入内置默认模板内容。
func (pl *PromptLoader) ensureFile() error {
	if _, err := os.Stat(pl.filePath); err == nil {
		return nil // 文件已存在
	}
	dir := filepath.Dir(pl.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建提示词文件目录失败 [%s]: %w", dir, err)
	}
	if err := os.WriteFile(pl.filePath, []byte(defaultPromptTemplate), 0o644); err != nil {
		return fmt.Errorf("写入默认提示词文件失败 [%s]: %w", pl.filePath, err)
	}
	pl.logger.Info("已生成默认提示词文件", slog.String("path", pl.filePath))
	return nil
}

// reload 从文件读取内容并重新编译模板，成功后以写锁原子替换当前模板。
func (pl *PromptLoader) reload() error {
	content, err := os.ReadFile(pl.filePath)
	if err != nil {
		return fmt.Errorf("读取提示词文件失败: %w", err)
	}
	tmpl, err := template.New("prompt").Parse(string(content))
	if err != nil {
		return fmt.Errorf("解析提示词模板失败: %w", err)
	}
	pl.mu.Lock()
	pl.tmpl = tmpl
	pl.mu.Unlock()
	return nil
}

// startWatcher 监听提示词文件所在目录（而非文件本身），
// 以兼容 vim/emacs 等编辑器的原子写操作（先删除后创建）。
func (pl *PromptLoader) startWatcher() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("创建文件监听器失败: %w", err)
	}
	dir := filepath.Dir(pl.filePath)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return fmt.Errorf("添加目录监听失败 [%s]: %w", dir, err)
	}
	pl.watcher = watcher
	go pl.watchLoop()
	return nil
}

// watchLoop 后台监听循环，过滤目标文件的变化事件并触发热重载。
func (pl *PromptLoader) watchLoop() {
	targetName := filepath.Base(pl.filePath)
	for {
		select {
		case <-pl.done:
			return
		case event, ok := <-pl.watcher.Events:
			if !ok {
				return
			}
			// 仅处理目标文件的事件（过滤目录中其他文件的变化）
			if filepath.Base(event.Name) != targetName {
				continue
			}
			// 写入或创建事件（覆盖写、原子写的最后一步均触发 Create 或 Write）
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				pl.logger.Info("提示词文件已变更，正在热重载",
					slog.String("path", event.Name),
					slog.String("op", event.Op.String()),
				)
				if err := pl.reload(); err != nil {
					pl.logger.Warn("提示词热重载失败，继续使用当前版本",
						slog.String("error", err.Error()),
					)
				} else {
					pl.logger.Info("提示词模板热重载成功，立即生效")
				}
			}
		case err, ok := <-pl.watcher.Errors:
			if !ok {
				return
			}
			pl.logger.Warn("提示词文件监听器错误", slog.String("error", err.Error()))
		}
	}
}
