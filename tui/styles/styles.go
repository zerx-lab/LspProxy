// Package styles 提供 TUI 界面的 lipgloss 样式定义。
package styles

import "github.com/charmbracelet/lipgloss"

var (
	// TitleStyle 标题样式：粗体 + 亮紫色
	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))

	// ActiveTabStyle 当前活跃标签：粗体 + 青绿色 + 下划线
	ActiveTabStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("86")).Underline(true)

	// TabStyle 非活跃标签：灰色
	TabStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	// SeparatorStyle 分隔线：深灰色
	SeparatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))

	// KeyStyle 键盘快捷键提示：中灰色
	KeyStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))

	// ValueStyle 普通值：近白色
	ValueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("255"))

	// SuccessStyle 成功状态：鲜绿色
	SuccessStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("82"))

	// ErrorStyle 错误状态：鲜红色
	ErrorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))

	// WarnStyle 警告状态：橙色
	WarnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))

	// BorderStyle 圆角边框：深灰色边框线
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("238"))

	// LabelStyle 表单标签：淡蓝灰色
	LabelStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("111"))

	// FocusedInputStyle 获得焦点的输入框：青色边框
	FocusedInputStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("86"))

	// DimStyle 次要文本：暗淡灰色
	DimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("243"))

	// HelpBarStyle 底部帮助栏：深灰背景 + 中灰文字
	HelpBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("243")).
			BorderTop(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("238"))

	// HeaderStyle 顶部头部栏
	HeaderStyle = lipgloss.NewStyle().
			BorderBottom(true).
			BorderStyle(lipgloss.NormalBorder()).
			BorderForeground(lipgloss.Color("238"))
)
