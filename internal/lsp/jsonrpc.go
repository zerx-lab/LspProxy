// Package lsp 实现 LSP（Language Server Protocol）协议的消息读写与处理。
// 本文件负责 JSON-RPC 2.0 over stdio 的帧格式解析与序列化。
//
// LSP 帧格式：
//
//	Content-Length: <N>\r\n
//	\r\n
//	<N 字节的 JSON 正文>
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RawMessage 是一个原始 JSON-RPC 消息的别名，保留为 json.RawMessage 以便后续按需解析。
type RawMessage = json.RawMessage

// ReadMessage 从 bufio.Reader 中读取一帧完整的 LSP 消息。
//
// 协议格式：
//
//	Content-Length: <N>\r\n
//	[其他可选 Header]\r\n
//	\r\n
//	<N 字节 JSON 正文>
//
// 返回原始 JSON 字节切片，调用方可自行反序列化。
func ReadMessage(r *bufio.Reader) ([]byte, error) {
	contentLength := -1

	// 逐行读取 HTTP 风格的头部，直到遇到空行（\r\n）
	for {
		// ReadString 会读取到 '\n'（含）为止
		line, err := r.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// 连接已关闭
				return nil, io.EOF
			}
			return nil, fmt.Errorf("读取 LSP 帧头部失败: %w", err)
		}

		// 去掉末尾的 \r\n 或 \n
		line = strings.TrimRight(line, "\r\n")

		// 空行代表头部结束
		if line == "" {
			break
		}

		// 解析 Content-Length 字段（大小写不敏感）
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "content-length:") {
			// 取冒号后的值并去除空白
			valStr := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(valStr)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("无效的 Content-Length 值: %q", valStr)
			}
			contentLength = n
		}
		// 其他头部字段（如 Content-Type）直接忽略
	}

	// 必须找到 Content-Length
	if contentLength < 0 {
		return nil, fmt.Errorf("LSP 帧缺少 Content-Length 头部")
	}

	// 按声明的长度读取 JSON 正文
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, fmt.Errorf("读取 LSP 消息体失败（期望 %d 字节）: %w", contentLength, err)
	}

	return body, nil
}

// WriteMessage 将 JSON 数据以 LSP 帧格式写入 writer。
//
// 写出格式：
//
//	Content-Length: <len(data)>\r\n
//	\r\n
//	<data>
//
// 整个帧通过一次 Write 调用写出，保证并发场景下帧的原子性（调用方需自行加锁）。
func WriteMessage(w io.Writer, data []byte) error {
	// 拼接帧头与正文，一次性写出以减少系统调用次数
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))

	// 预分配缓冲，避免多次 Write 在并发时被其他写入打断
	frame := make([]byte, 0, len(header)+len(data))
	frame = append(frame, header...)
	frame = append(frame, data...)

	if _, err := w.Write(frame); err != nil {
		return fmt.Errorf("写入 LSP 帧失败: %w", err)
	}

	return nil
}
