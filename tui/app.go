// Package tui 实现 LspProxy 的终端管理界面（TUI）。
// 使用 Bubble Tea 框架，提供"状态"、"配置"、"日志"三个视图标签页。
package tui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"LspProxy/internal/config"
	"LspProxy/tui/styles"
)

// ────────────────────────────────────────────────────────────
// 类型与常量
// ────────────────────────────────────────────────────────────

// viewTab 标识当前活跃的视图标签页
type viewTab int

const (
	TabStatus viewTab = iota // 状态视图：只读展示当前配置
	TabConfig                // 配置视图：表单编辑配置
	TabLog                   // 日志视图：滚动查看日志文件
)

// numConfigInputs 配置表单的字段总数
const numConfigInputs = 10

// 配置表单字段索引常量（与 configLabels / configGroups 对应）
const (
	fieldEngine             = iota // 翻译引擎 (google|openai)
	fieldBaseURL                   // OpenAI 兼容 API 地址
	fieldAPIKey                    // OpenAI API 密钥
	fieldModel                     // OpenAI 模型名
	fieldTargetLang                // 目标翻译语言
	fieldCacheSize                 // LRU 缓存大小
	fieldDictMaxEntries            // 磁盘词典最大条目数
	fieldTranslationTimeout        // 翻译等待超时（ms），0 表示无限等待
	fieldLogLevel                  // 日志级别
	fieldLogFile                   // 日志文件路径
)

// configLabels 各字段的显示标签（与字段索引一一对应）
var configLabels = [numConfigInputs]string{
	"翻译引擎         [ google | openai ]",
	"OpenAI BaseURL",
	"OpenAI API Key",
	"OpenAI 模型",
	"目标语言         [ zh-CN | ja | ko … ]",
	"内存缓存上限     [ MB，默认 30 ]",
	"磁盘词典上限     [ 条目数，0 = 不限，默认 100000 ]",
	"翻译等待超时     [ ms，0 = 无限等待，默认 600 ]",
	"日志级别         [ debug | info | warn | error ]",
	"日志文件路径",
}

// configPlaceholders 各字段的占位符提示文本
var configPlaceholders = [numConfigInputs]string{
	"google 或 openai",
	"https://api.openai.com/v1",
	"sk-xxxxxxxxxxxxxxxx",
	"gpt-4o-mini",
	"zh-CN",
	"30",
	"100000",
	"600",
	"info",
	"~/.local/share/lsp-proxy/proxy.log",
}

// ────────────────────────────────────────────────────────────
// Tea 消息类型
// ────────────────────────────────────────────────────────────

// tickMsg 定时触发日志文件刷新
type tickMsg time.Time

// logLoadedMsg 携带从日志文件读取到的行内容
type logLoadedMsg []string

// clearStatusMsg 通知 Model 清空底部状态提示
type clearStatusMsg struct{}

// ────────────────────────────────────────────────────────────
// Model
// ────────────────────────────────────────────────────────────

// Model 是 Bubble Tea 的主状态模型，实现 tea.Model 接口。
type Model struct {
	cfg     *config.Config // 当前配置（指针，编辑时直接修改后保存）
	cfgPath string         // 配置文件的磁盘路径

	activeTab viewTab // 当前活跃标签页

	width  int // 终端宽度（字符列数）
	height int // 终端高度（行数）

	// ── 日志视图 ──
	logViewport viewport.Model
	logLines    []string // 最近一次从日志文件读取到的行（原始行，未经 wrap 处理）
	logWrap     bool     // true = 超长行换行显示；false = 横向滚动（默认）

	// ── 配置表单 ──
	inputs   []textinput.Model // 各字段的 textinput 组件
	focusIdx int               // 当前获得焦点的字段索引

	// ── 底部操作反馈 ──
	statusMsg string // 反馈文本（空字符串表示无反馈）
	statusErr bool   // true 表示 statusMsg 是错误信息
}

// New 创建并初始化 TUI Model。
func New(cfg *config.Config, cfgPath string) Model {
	// 将配置值填入各输入框
	initValues := [numConfigInputs]string{
		cfg.Translate.Engine,
		cfg.Translate.OpenAI.BaseURL,
		cfg.Translate.OpenAI.APIKey,
		cfg.Translate.OpenAI.Model,
		cfg.Proxy.TargetLang,
		strconv.Itoa(cfg.Proxy.CacheSize),
		strconv.Itoa(cfg.Proxy.DictMaxEntries),
		strconv.Itoa(cfg.Proxy.TranslationTimeout),
		cfg.Log.Level,
		cfg.Log.File,
	}

	inputs := make([]textinput.Model, numConfigInputs)
	for i := range inputs {
		ti := textinput.New()
		ti.Placeholder = configPlaceholders[i]
		ti.SetValue(initValues[i])
		ti.Width = 52
		if i == fieldAPIKey {
			// 密钥字段：隐藏显示
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '•'
		}
		inputs[i] = ti
	}

	// 初始化 viewport（尺寸会在 WindowSizeMsg 中更新）
	// 启用横向滚动（步长 4 列），避免长日志行被截断；左右方向键及鼠标横向滚轮均可触发
	vp := viewport.New(80, 20)
	vp.SetHorizontalStep(4)
	vp.SetContent(styles.DimStyle.Render("（切换到「日志」标签页以加载日志）"))

	return Model{
		cfg:         cfg,
		cfgPath:     cfgPath,
		activeTab:   TabStatus,
		inputs:      inputs,
		focusIdx:    0,
		logViewport: vp,
	}
}

// ────────────────────────────────────────────────────────────
// tea.Model 接口实现
// ────────────────────────────────────────────────────────────

// Init 返回初始命令：启动定时日志刷新。
func (m Model) Init() tea.Cmd {
	return tickCmd()
}

// Update 是消息处理的入口，根据消息类型分发到对应处理逻辑。
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// 终端尺寸变化：更新 Model 中的宽高及 viewport 尺寸
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// viewport 高度 = 总高 - 顶部 header(4 行) - 日志子标题(2 行) - 底部 footer(3 行)
		vpH := msg.Height - 9
		if vpH < 3 {
			vpH = 3
		}
		m.logViewport.Width = msg.Width
		m.logViewport.Height = vpH
		// 宽度变化后，若处于换行模式需按新宽度重新 wrap 内容
		if len(m.logLines) > 0 {
			m.setLogViewportContent(false)
		}
		return m, nil

	// 定时器触发：刷新日志文件内容，并安排下一次定时
	case tickMsg:
		return m, tea.Batch(loadLogCmd(m.cfg.Log.File), tickCmd())

	// 日志内容已读取完毕：更新 viewport
	case logLoadedMsg:
		m.logLines = []string(msg)
		m.setLogViewportContent(true)
		return m, nil

	// 清除底部状态提示
	case clearStatusMsg:
		m.statusMsg = ""
		m.statusErr = false
		return m, nil

	// 键盘事件：路由到当前视图的处理函数
	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey 将键盘事件分发到对应视图的处理逻辑。
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Ctrl+C 全局退出
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	switch m.activeTab {
	case TabStatus:
		return m.handleStatusKey(msg.String())
	case TabConfig:
		return m.handleConfigKey(msg)
	case TabLog:
		return m.handleLogKey(msg)
	}
	return m, nil
}

// ── 状态视图键盘处理 ──────────────────────────────────────────

// handleStatusKey 处理状态视图中的键盘事件。
func (m Model) handleStatusKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q":
		return m, tea.Quit
	case "2", "tab":
		return m.switchToConfig()
	case "3":
		return m.switchToLog()
	}
	return m, nil
}

// ── 配置视图键盘处理 ──────────────────────────────────────────

// handleConfigKey 处理配置视图中的键盘事件。
// 除导航与控制键外，其余按键均透传给当前聚焦的输入框。
func (m Model) handleConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		// 退出配置视图，不保存
		m.blurAll()
		m.activeTab = TabStatus
		return m, nil

	case "tab", "down":
		m.nextFocus()
		return m, textinput.Blink

	case "shift+tab", "up":
		m.prevFocus()
		return m, textinput.Blink

	case "ctrl+s":
		// 强制保存
		return m.doSaveConfig()

	case "enter":
		if m.focusIdx < numConfigInputs-1 {
			// 非末尾字段：移动焦点到下一个
			m.nextFocus()
			return m, textinput.Blink
		}
		// 末尾字段：保存
		return m.doSaveConfig()

	default:
		// 其余按键（包括数字、字母等）透传给当前输入框
		var cmd tea.Cmd
		m.inputs[m.focusIdx], cmd = m.inputs[m.focusIdx].Update(msg)
		return m, cmd
	}
}

// ── 日志视图键盘处理 ──────────────────────────────────────────

// handleLogKey 处理日志视图中的键盘事件。
func (m Model) handleLogKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q":
		return m, tea.Quit
	case "1":
		m.activeTab = TabStatus
		return m, nil
	case "2":
		return m.switchToConfig()
	case "tab":
		m.activeTab = TabStatus
		return m, nil
	case "g":
		m.logViewport.GotoTop()
		return m, nil
	case "G":
		m.logViewport.GotoBottom()
		return m, nil
	case "D":
		// 清空日志文件，并立即刷新视图
		return m, clearLogCmd(m.cfg.Log.File)
	case "W":
		// 切换换行/横向滚动模式
		m.logWrap = !m.logWrap
		if m.logWrap {
			// 进入换行模式：禁用横向滚动，重绘内容
			m.logViewport.SetHorizontalStep(0)
			m.logViewport.SetXOffset(0)
		} else {
			// 恢复横向滚动模式
			m.logViewport.SetHorizontalStep(4)
		}
		m.setLogViewportContent(false)
		return m, nil
	default:
		// j/k、方向键、Page Up/Down 由 viewport 内部处理
		var cmd tea.Cmd
		m.logViewport, cmd = m.logViewport.Update(msg)
		return m, cmd
	}
}

// ── 标签页切换辅助 ────────────────────────────────────────────

// switchToConfig 切换到配置视图并聚焦当前字段。
func (m Model) switchToConfig() (tea.Model, tea.Cmd) {
	m.blurAll()
	m.activeTab = TabConfig
	m.inputs[m.focusIdx].Focus()
	return m, textinput.Blink
}

// switchToLog 切换到日志视图并触发一次日志加载。
func (m Model) switchToLog() (tea.Model, tea.Cmd) {
	m.blurAll()
	m.activeTab = TabLog
	return m, loadLogCmd(m.cfg.Log.File)
}

// ── 焦点管理辅助 ──────────────────────────────────────────────

// nextFocus 将焦点移到下一个输入框（循环）。
func (m *Model) nextFocus() {
	m.inputs[m.focusIdx].Blur()
	m.focusIdx = (m.focusIdx + 1) % numConfigInputs
	m.inputs[m.focusIdx].Focus()
}

// prevFocus 将焦点移到上一个输入框（循环）。
func (m *Model) prevFocus() {
	m.inputs[m.focusIdx].Blur()
	m.focusIdx = (m.focusIdx - 1 + numConfigInputs) % numConfigInputs
	m.inputs[m.focusIdx].Focus()
}

// blurAll 让所有输入框失去焦点。
func (m *Model) blurAll() {
	for i := range m.inputs {
		m.inputs[i].Blur()
	}
}

// ── 配置保存 ──────────────────────────────────────────────────

// doSaveConfig 从表单输入框读取值，更新 Config 结构体，并写入磁盘。
func (m Model) doSaveConfig() (tea.Model, tea.Cmd) {
	m.cfg.Translate.Engine = strings.TrimSpace(m.inputs[fieldEngine].Value())
	m.cfg.Translate.OpenAI.BaseURL = strings.TrimSpace(m.inputs[fieldBaseURL].Value())
	m.cfg.Translate.OpenAI.APIKey = strings.TrimSpace(m.inputs[fieldAPIKey].Value())
	m.cfg.Translate.OpenAI.Model = strings.TrimSpace(m.inputs[fieldModel].Value())
	m.cfg.Proxy.TargetLang = strings.TrimSpace(m.inputs[fieldTargetLang].Value())

	if n, err := strconv.Atoi(strings.TrimSpace(m.inputs[fieldCacheSize].Value())); err == nil && n > 0 {
		m.cfg.Proxy.CacheSize = n
	}

	if n, err := strconv.Atoi(strings.TrimSpace(m.inputs[fieldDictMaxEntries].Value())); err == nil && n >= 0 {
		m.cfg.Proxy.DictMaxEntries = n
	}

	if n, err := strconv.Atoi(strings.TrimSpace(m.inputs[fieldTranslationTimeout].Value())); err == nil && n >= 0 {
		m.cfg.Proxy.TranslationTimeout = n
	}

	m.cfg.Log.Level = strings.TrimSpace(m.inputs[fieldLogLevel].Value())
	m.cfg.Log.File = strings.TrimSpace(m.inputs[fieldLogFile].Value())

	if err := m.cfg.Save(m.cfgPath); err != nil {
		m.statusMsg = fmt.Sprintf("✗ 保存失败：%v", err)
		m.statusErr = true
	} else {
		m.statusMsg = fmt.Sprintf("✓ 已保存至 %s", m.cfgPath)
		m.statusErr = false
	}

	// 3 秒后自动清除提示
	return m, clearStatusCmd()
}

// ────────────────────────────────────────────────────────────
// View：渲染整个 TUI 界面
// ────────────────────────────────────────────────────────────

// View 渲染完整的 TUI 界面字符串。
func (m Model) View() string {
	if m.width == 0 {
		return "正在初始化…"
	}

	header := m.renderHeader()

	var body string
	switch m.activeTab {
	case TabStatus:
		body = m.renderStatus()
	case TabConfig:
		body = m.renderConfig()
	case TabLog:
		body = m.renderLog()
	}

	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// ── 顶部 Header ───────────────────────────────────────────────

// renderHeader 渲染顶部标题栏与标签页行。
func (m Model) renderHeader() string {
	// 标题行：左侧品牌 + 右侧退出提示
	brand := styles.TitleStyle.Render("🔄 LspProxy")
	quitHint := styles.KeyStyle.Render("[q] 退出")
	brandW := lipgloss.Width(brand)
	hintW := lipgloss.Width(quitHint)
	gapW := m.width - brandW - hintW - 2
	if gapW < 1 {
		gapW = 1
	}
	titleLine := " " + brand + strings.Repeat(" ", gapW) + quitHint

	sep := styles.SeparatorStyle.Render(strings.Repeat("─", m.width))

	tabLine := m.renderTabs()

	return lipgloss.JoinVertical(lipgloss.Left,
		titleLine,
		sep,
		tabLine,
		sep,
	)
}

// renderTabs 渲染标签页按钮行。
func (m Model) renderTabs() string {
	defs := []struct {
		tab   viewTab
		label string
	}{
		{TabStatus, "[1] 状态"},
		{TabConfig, "[2] 配置"},
		{TabLog, "[3] 日志"},
	}

	var parts []string
	for _, d := range defs {
		if d.tab == m.activeTab {
			parts = append(parts, styles.ActiveTabStyle.Render(d.label))
		} else {
			parts = append(parts, styles.TabStyle.Render(d.label))
		}
	}
	return "  " + strings.Join(parts, "   ")
}

// ── 底部 Footer ───────────────────────────────────────────────

// renderFooter 渲染底部帮助栏，以及操作反馈消息（若有）。
func (m Model) renderFooter() string {
	sep := styles.SeparatorStyle.Render(strings.Repeat("─", m.width))
	// 帮助文本超出终端宽度时自动折行（留 2 列缩进余量）
	helpContent := m.helpText()
	if m.width > 4 {
		helpContent = ansi.Wrap(helpContent, m.width-2, "")
	}
	help := "  " + styles.KeyStyle.Render(helpContent)

	lines := []string{sep, help}

	if m.statusMsg != "" {
		var s string
		if m.statusErr {
			s = "  " + styles.ErrorStyle.Render(m.statusMsg)
		} else {
			s = "  " + styles.SuccessStyle.Render(m.statusMsg)
		}
		lines = append(lines, s)
	}

	return strings.Join(lines, "\n")
}

// helpText 根据当前视图返回帮助提示文本。
func (m Model) helpText() string {
	switch m.activeTab {
	case TabStatus:
		return "1/2/3 切换标签  •  Tab 下一个  •  q 退出"
	case TabConfig:
		return "Tab/↓ 下一字段  •  ↑ 上一字段  •  Enter 确认  •  Ctrl+S 保存  •  Esc 返回"
	case TabLog:
		wrapHint := "W 开启换行"
		if m.logWrap {
			wrapHint = "W 关闭换行"
		}
		return "↑/↓/j/k/PgUp/PgDn 上下滚动  •  ←/→ 左右滚动  •  g 顶部  •  G 底部  •  " + wrapHint + "  •  D 清空日志  •  1/2/3 切换  •  q 退出"
	}
	return ""
}

// ── 状态视图 ──────────────────────────────────────────────────

// renderStatus 渲染只读的配置摘要视图。
func (m Model) renderStatus() string {
	var sb strings.Builder

	dividerW := 44
	if m.width-4 < dividerW {
		dividerW = m.width - 4
	}

	// 辅助：渲染一行"标签 + 值"
	kv := func(label, value string) string {
		l := styles.LabelStyle.Render(fmt.Sprintf("  %-22s", label))
		v := styles.ValueStyle.Render(value)
		return l + v
	}

	section := func(title string) string {
		t := styles.TitleStyle.Render(title)
		d := styles.SeparatorStyle.Render(strings.Repeat("─", dividerW))
		return "\n  " + t + "\n  " + d
	}

	// ── 翻译配置 ──
	sb.WriteString(section("翻译配置"))
	sb.WriteString("\n")

	engine := m.cfg.Translate.Engine
	if engine == "" {
		engine = "google（默认）"
	}
	sb.WriteString(kv("引擎", engine))
	sb.WriteString("\n")
	sb.WriteString(kv("目标语言", m.cfg.Proxy.TargetLang))
	sb.WriteString("\n")
	sb.WriteString(kv("内存缓存上限", strconv.Itoa(m.cfg.Proxy.CacheSize)+" MB"))
	sb.WriteString("\n")

	dictMaxDisplay := strconv.Itoa(m.cfg.Proxy.DictMaxEntries) + " 条"
	if m.cfg.Proxy.DictMaxEntries == 0 {
		dictMaxDisplay = "不限制"
	}
	sb.WriteString(kv("磁盘词典上限", dictMaxDisplay))
	sb.WriteString("\n")

	timeoutDisplay := strconv.Itoa(m.cfg.Proxy.TranslationTimeout) + " ms"
	if m.cfg.Proxy.TranslationTimeout == 0 {
		timeoutDisplay = "无限等待"
	}
	sb.WriteString(kv("翻译等待超时", timeoutDisplay))
	sb.WriteString("\n")

	// ── OpenAI 配置 ──
	sb.WriteString(section("OpenAI 配置"))
	sb.WriteString("\n")
	sb.WriteString(kv("BaseURL", m.cfg.Translate.OpenAI.BaseURL))
	sb.WriteString("\n")
	sb.WriteString(kv("模型", m.cfg.Translate.OpenAI.Model))
	sb.WriteString("\n")

	apiKey := m.cfg.Translate.OpenAI.APIKey
	var apiKeyDisplay string
	if apiKey == "" {
		apiKeyDisplay = styles.DimStyle.Render("（未配置）")
	} else if len(apiKey) > 8 {
		apiKeyDisplay = apiKey[:8] + "••••••••"
	} else {
		apiKeyDisplay = strings.Repeat("•", len(apiKey))
	}
	sb.WriteString(kv("API Key", apiKeyDisplay))
	sb.WriteString("\n")

	// ── 日志配置 ──
	sb.WriteString(section("日志配置"))
	sb.WriteString("\n")
	sb.WriteString(kv("级别", m.cfg.Log.Level))
	sb.WriteString("\n")
	sb.WriteString(kv("文件", m.cfg.Log.File))
	sb.WriteString("\n")

	// ── 提示 ──
	sb.WriteString("\n  ")
	sb.WriteString(styles.DimStyle.Render("按 [2] 进入配置视图编辑以上参数，按 [3] 查看运行日志"))
	sb.WriteString("\n")

	return sb.String()
}

// ── 配置视图 ──────────────────────────────────────────────────

// renderConfig 渲染可编辑的配置表单视图。
func (m Model) renderConfig() string {
	var sb strings.Builder

	sb.WriteString("\n  ")
	sb.WriteString(styles.TitleStyle.Render("编辑配置"))
	sb.WriteString("\n\n")

	// 字段分组：翻译(0-3)、代理(4-5)、日志(6-7)
	groups := []struct {
		title  string
		fields []int
	}{
		{"翻译引擎", []int{fieldEngine, fieldBaseURL, fieldAPIKey, fieldModel}},
		{"代理设置", []int{fieldTargetLang, fieldCacheSize, fieldDictMaxEntries, fieldTranslationTimeout}},
		{"日志设置", []int{fieldLogLevel, fieldLogFile}},
	}

	for _, grp := range groups {
		sb.WriteString("  ")
		sb.WriteString(styles.SeparatorStyle.Render("── " + grp.title + " "))
		sb.WriteString("\n")

		for _, idx := range grp.fields {
			label := configLabels[idx]
			var labelLine string
			if idx == m.focusIdx {
				labelLine = "  " + styles.FocusedInputStyle.Render("▶ "+label)
			} else {
				labelLine = "  " + styles.LabelStyle.Render("  "+label)
			}
			sb.WriteString(labelLine + "\n")
			sb.WriteString("    " + m.inputs[idx].View() + "\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("  ")
	sb.WriteString(styles.DimStyle.Render("Ctrl+S 或最后一项 Enter 保存  •  Esc 返回不保存"))
	sb.WriteString("\n")

	return sb.String()
}

// ── 日志视图 ──────────────────────────────────────────────────

// setLogViewportContent 将 m.logLines 写入 viewport。
// 换行模式下对每行调用 ansi.Wrap 按终端宽度折行；横向滚动模式下原样写入。
// gotoBottom 为 true 时写入后自动滚到底部（新内容到达时使用）。
func (m *Model) setLogViewportContent(gotoBottom bool) {
	if m.logWrap && m.logViewport.Width > 0 {
		// 换行模式：每行独立 wrap 后重新拼接
		wrapped := make([]string, 0, len(m.logLines))
		for _, line := range m.logLines {
			wrapped = append(wrapped, ansi.Wrap(line, m.logViewport.Width, ""))
		}
		m.logViewport.SetContent(strings.Join(wrapped, "\n"))
	} else {
		m.logViewport.SetContent(strings.Join(m.logLines, "\n"))
	}
	if gotoBottom {
		m.logViewport.GotoBottom()
	}
}

// renderLog 渲染日志视图（使用 viewport 支持上下滚动）。
func (m Model) renderLog() string {
	wrapIndicator := ""
	if m.logWrap {
		wrapIndicator = "  " + styles.DimStyle.Render("[换行]")
	}
	lineCount := styles.DimStyle.Render(fmt.Sprintf("（共 %d 行）", len(m.logLines)))
	filePath := styles.DimStyle.Render(m.cfg.Log.File)
	header := "  " + styles.TitleStyle.Render("日志") +
		wrapIndicator +
		"  " + lineCount +
		"  " + filePath + "\n\n"

	return header + m.logViewport.View()
}

// ────────────────────────────────────────────────────────────
// Tea 命令函数
// ────────────────────────────────────────────────────────────

// tickCmd 返回一个 2 秒后触发的定时命令，用于周期性刷新日志。
func tickCmd() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// clearStatusCmd 返回一个 3 秒后触发的命令，用于自动清除底部状态提示。
func clearStatusCmd() tea.Cmd {
	return tea.Tick(3*time.Second, func(_ time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

// clearLogCmd 返回异步清空日志文件的命令，清空后触发一次重新加载。
func clearLogCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := os.Truncate(path, 0); err != nil && !os.IsNotExist(err) {
			return logLoadedMsg([]string{
				styles.ErrorStyle.Render("清空日志文件失败：" + err.Error()),
			})
		}
		// 清空成功，返回空内容提示
		return logLoadedMsg([]string{
			styles.DimStyle.Render("（日志已清空）"),
		})
	}
}

// loadLogCmd 返回异步读取日志文件的命令。
// 读取最后 500 行，以避免日志过大时卡顿。
func loadLogCmd(path string) tea.Cmd {
	return func() tea.Msg {
		lines, err := readLastLines(path, 500)
		if err != nil {
			return logLoadedMsg([]string{
				styles.ErrorStyle.Render("读取日志文件失败：" + err.Error()),
			})
		}
		if len(lines) == 0 {
			return logLoadedMsg([]string{
				styles.DimStyle.Render("（日志文件为空）"),
			})
		}
		return logLoadedMsg(lines)
	}
}

// ────────────────────────────────────────────────────────────
// 辅助函数
// ────────────────────────────────────────────────────────────

// readLastLines 从文件中读取最后 n 行内容。
// 若文件不存在，返回友好提示而非错误。
func readLastLines(path string, n int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{"（日志文件尚未创建，代理运行后将自动生成）"}, nil
		}
		return nil, fmt.Errorf("打开日志文件失败: %w", err)
	}
	defer f.Close()

	// 使用较大的扫描缓冲区，以防单行日志过长
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 1<<20) // 1 MiB
	scanner.Buffer(buf, 1<<20)

	// 使用环形缓冲区，只保留最后 n 行，避免大文件全部读入内存
	ring := make([]string, n)
	idx := 0   // 下一次写入位置
	total := 0 // 已读取的总行数

	for scanner.Scan() {
		ring[idx] = scanner.Text()
		idx = (idx + 1) % n
		total++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("读取日志内容失败: %w", err)
	}

	if total == 0 {
		return nil, nil
	}

	// 从环形缓冲区中按顺序提取结果
	count := total
	if count > n {
		count = n
	}
	result := make([]string, 0, count)
	start := 0
	if total >= n {
		start = idx // idx 指向最旧的一行
	}
	for i := 0; i < count; i++ {
		result = append(result, ring[(start+i)%n])
	}

	return result, nil
}

// ────────────────────────────────────────────────────────────
// 程序入口
// ────────────────────────────────────────────────────────────

// Run 创建并启动 Bubble Tea TUI 程序。
// 使用备用屏幕（AltScreen），退出后自动恢复原终端内容。
func Run(cfg *config.Config, cfgPath string) error {
	m := New(cfg, cfgPath)
	p := tea.NewProgram(
		m,
		tea.WithAltScreen(), // 使用备用屏幕，退出后恢复原终端
		// 注意：不启用 WithMouseCellMotion()，
		// 因为应用级鼠标捕获会阻止终端原生文字选择（拖选复制）。
		// 日志视图通过键盘（j/k/↑/↓/PgUp/PgDn/g/G）滚动。
	)
	_, err := p.Run()
	return err
}
