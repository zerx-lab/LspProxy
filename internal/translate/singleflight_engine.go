// Package translate 提供翻译引擎接口及其装饰器实现。
package translate

import (
	"context"

	"golang.org/x/sync/singleflight"
)

// SingleflightEngine 是翻译引擎的并发合并装饰器。
//
// 核心职责：确保同一时刻对相同文本（经 [NormalizeKey] 规范化后）的并发翻译请求
// 只发起一次底层 API 调用，其余并发请求阻塞等待并共享同一结果。
//
// 典型场景：用户快速移动光标触发多次 hover，连续产生 N 个对相同文档文本的翻译请求。
// 没有此层时，N 个后台 goroutine 会同时调用翻译 API，浪费 N-1 次 API 配额。
// 有此层后，N 个请求中只有 1 次真实 API 调用，其余共享结果。
//
// 位置：在引擎链路中位于 DictEngine/CachedEngine 之外、GlossaryEngine 之内，
// 确保对所有需要网络 IO 的路径都能合并请求：
//
//	GlossaryEngine → SingleflightEngine → DictEngine（内存→磁盘→API）
//
// 注意：GlossaryEngine 不需要此层，因为词汇本命中是纯内存操作，无 IO 开销。
type SingleflightEngine struct {
	inner Engine
	group singleflight.Group
}

// NewSingleflightEngine 创建一个并发合并装饰器，包装底层引擎 inner。
func NewSingleflightEngine(inner Engine) *SingleflightEngine {
	return &SingleflightEngine{inner: inner}
}

// Translate 对相同文本的并发翻译请求进行合并：
//   - 若当前无同 key 的进行中请求：发起底层翻译调用
//   - 若已有同 key 的进行中请求：阻塞等待，共享其结果
//
// sfKey 由规范化文本（[NormalizeKey]）和目标语言拼接而成，
// 保证语义等价的文本（仅空白差异）共享同一次 API 调用。
//
// context 语义说明：
//   - 底层调用使用的是**第一个到达的请求**的 ctx。
//   - 若调用方的 ctx 在等待期间被取消，本次 Translate 返回 ctx.Err()，
//     但底层翻译调用**仍继续执行**直至完成或其自身 ctx 超时。
//     这一行为是有意为之：后台缓存预热应不受单次请求生命周期影响。
func (s *SingleflightEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	// sfKey 使用规范化后的文本，语义等价的文本共享同一次 API 调用
	sfKey := NormalizeKey(text) + "\x00" + targetLang

	v, err, _ := s.group.Do(sfKey, func() (any, error) {
		return s.inner.Translate(ctx, text, targetLang)
	})

	if err != nil {
		// 若调用方 ctx 已取消，优先返回 ctx 错误（语义更准确）
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", err
	}

	// 底层调用成功，但调用方 ctx 可能在等待 singleflight 期间已被取消
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	return v.(string), nil
}

// Name 返回带装饰标识的引擎名称，便于日志和调试。
func (s *SingleflightEngine) Name() string {
	return s.inner.Name() + "(singleflight)"
}
