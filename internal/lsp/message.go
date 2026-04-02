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
type CompletionItem struct {
	// Label 补全项显示标签（在列表中展示的文本）
	Label string `json:"label"`
	// Documentation 补全项的详细文档，可以是 string 或 MarkupContent
	Documentation json.RawMessage `json:"documentation,omitempty"`
	// Detail 补全项的简短描述（通常显示在标签右侧）
	Detail string `json:"detail,omitempty"`
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
	// Message 诊断的描述文本（需要翻译的核心字段）
	Message string `json:"message"`
	// Source 产生该诊断的工具或语言服务器名称（如 "rustc"、"clippy"）
	Source string `json:"source,omitempty"`
}

// PublishDiagnosticsParams 对应 textDocument/publishDiagnostics 通知的参数。
type PublishDiagnosticsParams struct {
	// URI 对应的文档 URI
	URI string `json:"uri"`
	// Diagnostics 该文档的全量诊断列表
	Diagnostics []Diagnostic `json:"diagnostics"`
}
