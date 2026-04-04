// Package lsp 提供 LSP（Language Server Protocol）协议的消息类型定义。
// 本文件定义 JSON-RPC 2.0 基础结构及翻译相关的具体消息类型。
package lsp

import "encoding/json"

// BaseMessage 表示一个完整的 JSON-RPC 2.0 消息。
// 请求、响应、通知均使用同一结构，通过字段组合来区分。
type BaseMessage struct {
	// JSONRPC 协议版本，固定为 "2.0"
	JSONRPC string `json:"jsonrpc"`
	// ID 请求/响应标识符，可以是整数、字符串或 null；通知中不存在此字段
	ID json.RawMessage `json:"id,omitempty"`
	// Method 方法名称；请求和通知中存在，响应中不存在
	Method string `json:"method,omitempty"`
	// Params 请求/通知的参数，保留为原始 JSON 以便按需解析
	Params json.RawMessage `json:"params,omitempty"`
	// Result 成功响应的结果，保留为原始 JSON 以便按需解析
	Result json.RawMessage `json:"result,omitempty"`
	// Error 失败响应的错误信息
	Error *ResponseError `json:"error,omitempty"`
}

// ResponseError 表示 JSON-RPC 错误对象
type ResponseError struct {
	// Code 错误代码（遵循 JSON-RPC 及 LSP 规范中的错误码定义）
	Code int `json:"code"`
	// Message 人类可读的错误描述
	Message string `json:"message"`
}

// isNullOrEmpty 判断 RawMessage 是否为空或 JSON null
func isNullOrEmpty(r json.RawMessage) bool {
	if len(r) == 0 {
		return true
	}
	// JSON null 字面量
	if string(r) == "null" {
		return true
	}
	return false
}

// IsRequest 判断消息是否为请求（同时具有 Method 和非空 ID）
func (m *BaseMessage) IsRequest() bool {
	return m.Method != "" && !isNullOrEmpty(m.ID)
}

// IsNotification 判断消息是否为通知（具有 Method 但没有 ID）
func (m *BaseMessage) IsNotification() bool {
	return m.Method != "" && isNullOrEmpty(m.ID)
}

// IsResponse 判断消息是否为响应（没有 Method 但具有非空 ID）
func (m *BaseMessage) IsResponse() bool {
	return m.Method == "" && !isNullOrEmpty(m.ID)
}

// ---- 具体消息类型（仅包含翻译所需字段）----

// MarkupContent 用于 hover 结果和 completion item 的文档字段。
// LSP 规范中 kind 为 "plaintext" 或 "markdown"。
type MarkupContent struct {
	// Kind 内容格式："plaintext" 或 "markdown"
	Kind string `json:"kind"`
	// Value 实际文本内容
	Value string `json:"value"`
}

// HoverResult 对应 textDocument/hover 请求的响应结果。
// Contents 字段可能为以下三种形式之一：
//   - string（纯文本）
//   - MarkupContent（带格式的文档）
//   - []MarkedString（已废弃，旧版 LSP 兼容）
type HoverResult struct {
	// Contents 悬浮文档内容，保留为原始 JSON 以便统一处理多种格式
	Contents json.RawMessage `json:"contents"`
	// Range 触发 hover 的源码范围（可选）
	Range json.RawMessage `json:"range,omitempty"`
}

// CompletionItem 表示自动补全列表中的一个条目。
// 注意：必须保留所有 LSP 规范字段，否则序列化时会丢弃控制光标行为的关键信息。
type CompletionItem struct {
	// Label 补全项显示标签（在列表中展示的文本）
	Label string `json:"label"`
	// LabelDetails 标签的额外细节（LSP 3.17+）
	LabelDetails json.RawMessage `json:"labelDetails,omitempty"`
	// Kind 补全类型（1=Text, 2=Method, 3=Function, ...）
	Kind int `json:"kind,omitempty"`
	// Tags 补全标签（1=Deprecated）
	Tags json.RawMessage `json:"tags,omitempty"`
	// Detail 补全项的简短描述（通常显示在标签右侧）
	Detail string `json:"detail,omitempty"`
	// Documentation 补全项的详细文档，可以是 string 或 MarkupContent
	Documentation json.RawMessage `json:"documentation,omitempty"`
	// Deprecated 是否已废弃（旧版，建议用 Tags 代替）
	Deprecated bool `json:"deprecated,omitempty"`
	// Preselect 是否预先选中此条目
	Preselect bool `json:"preselect,omitempty"`
	// SortText 排序用文本（缺省时使用 Label）
	SortText string `json:"sortText,omitempty"`
	// FilterText 过滤用文本（缺省时使用 Label）
	FilterText string `json:"filterText,omitempty"`
	// InsertText 实际插入的文本（缺省时使用 Label）
	InsertText string `json:"insertText,omitempty"`
	// InsertTextFormat 插入文本格式：1=PlainText, 2=Snippet（含占位符和光标控制）
	InsertTextFormat int `json:"insertTextFormat,omitempty"`
	// InsertTextMode 插入时的空白处理模式（LSP 3.16+）
	InsertTextMode int `json:"insertTextMode,omitempty"`
	// TextEdit 精确替换操作，优先级高于 InsertText（控制光标位置的关键字段）
	TextEdit json.RawMessage `json:"textEdit,omitempty"`
	// TextEditText 当 TextEdit 为 InsertReplaceEdit 时使用（LSP 3.16+）
	TextEditText string `json:"textEditText,omitempty"`
	// AdditionalTextEdits 补全后额外的文本编辑（如自动添加 import）
	AdditionalTextEdits json.RawMessage `json:"additionalTextEdits,omitempty"`
	// CommitCharacters 触发提交的字符列表
	CommitCharacters json.RawMessage `json:"commitCharacters,omitempty"`
	// Command 补全后自动执行的命令（如触发签名帮助）
	Command json.RawMessage `json:"command,omitempty"`
	// Data 供 completionItem/resolve 请求使用的扩展数据
	Data json.RawMessage `json:"data,omitempty"`
}

// CompletionList 对应 textDocument/completion 请求的响应结果。
// 服务端可以直接返回 []CompletionItem 或本结构体，两种形式均需处理。
type CompletionList struct {
	// IsIncomplete 为 true 时表示列表不完整，继续输入后需要重新请求
	IsIncomplete bool `json:"isIncomplete"`
	// Items 补全条目列表
	Items []CompletionItem `json:"items"`
}

// SignatureInformation 表示一个函数/方法签名的信息。
type SignatureInformation struct {
	// Label 签名的文本表示（通常为完整函数签名字符串）
	Label string `json:"label"`
	// Documentation 签名的详细文档，可以是 string 或 MarkupContent
	Documentation json.RawMessage `json:"documentation,omitempty"`
}

// SignatureHelp 对应 textDocument/signatureHelp 请求的响应结果。
type SignatureHelp struct {
	// Signatures 所有可能的函数签名列表
	Signatures []SignatureInformation `json:"signatures"`
	// ActiveSignature 当前激活的签名索引（默认 0）
	ActiveSignature int `json:"activeSignature,omitempty"`
	// ActiveParameter 当前激活的参数索引（默认 0）
	ActiveParameter int `json:"activeParameter,omitempty"`
}

// Diagnostic 表示源码中的一条诊断信息（错误、警告、提示等）。
type Diagnostic struct {
	// Range 诊断覆盖的源码范围
	Range json.RawMessage `json:"range"`
	// Severity 诊断严重程度：1=Error, 2=Warning, 3=Information, 4=Hint
	Severity int `json:"severity,omitempty"`
	// Code 诊断代码，可以是字符串或整数（如 "inactive-code"、E0001）
	Code json.RawMessage `json:"code,omitempty"`
	// CodeDescription 指向诊断详情的链接（LSP 3.16+）
	CodeDescription json.RawMessage `json:"codeDescription,omitempty"`
	// Source 产生该诊断的工具或语言服务器名称（如 "rustc"、"clippy"）
	Source string `json:"source,omitempty"`
	// Message 诊断的描述文本（需要翻译的核心字段）
	Message string `json:"message"`
	// Tags 诊断标签：1=Unnecessary（灰显非活跃代码），2=Deprecated
	Tags json.RawMessage `json:"tags,omitempty"`
	// RelatedInformation 关联的补充信息（如变量声明位置等）
	RelatedInformation json.RawMessage `json:"relatedInformation,omitempty"`
	// Data 供代码动作使用的扩展数据（LSP 3.16+）
	Data json.RawMessage `json:"data,omitempty"`
}

// PublishDiagnosticsParams 对应 textDocument/publishDiagnostics 通知的参数。
type PublishDiagnosticsParams struct {
	// URI 对应的文档 URI
	URI string `json:"uri"`
	// Version 文档版本号（可选，LSP 3.15+）
	Version *int `json:"version,omitempty"`
	// Diagnostics 该文档的全量诊断列表
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// DocumentDiagnosticReport 对应 textDocument/diagnostic 请求（拉取模式）的响应结果。
// LSP 3.17+ 规范：编辑器主动拉取诊断，而非服务端推送。
// rust-analyzer、clangd 等现代 LSP 服务器会同时支持拉取和推送两种模式。
type DocumentDiagnosticReport struct {
	// Kind 报告类型："full"（全量）或 "unchanged"（未变化，无需重新渲染）
	Kind string `json:"kind"`
	// ResultID 结果标识符，供下次请求时作为 previousResultId 传入（增量更新优化）
	ResultID string `json:"resultId,omitempty"`
	// Items 诊断条目列表（kind 为 "full" 时存在）
	Items []Diagnostic `json:"items,omitempty"`
}
