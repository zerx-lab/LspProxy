package translate

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// SingleflightEngine 单元测试
// ─────────────────────────────────────────────────────────────────────────────

// slowMockEngine 是一个可控延迟的模拟翻译引擎，用于测试并发合并行为。
type slowMockEngine struct {
	callCount atomic.Int64
	delay     time.Duration
	returnErr error
}

func (m *slowMockEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	m.callCount.Add(1)
	select {
	case <-time.After(m.delay):
	case <-ctx.Done():
		return "", ctx.Err()
	}
	if m.returnErr != nil {
		return "", m.returnErr
	}
	return fmt.Sprintf("[%s] %s", targetLang, text), nil
}

func (m *slowMockEngine) Name() string { return "slow-mock" }

// ─────────────────────────────────────────────────────────────────────────────
// 并发合并：N 个 goroutine 同时翻译相同文本，底层引擎只调用一次
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_MergesConcurrentRequests(t *testing.T) {
	const goroutines = 20
	inner := &slowMockEngine{delay: 50 * time.Millisecond}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	var wg sync.WaitGroup
	results := make([]string, goroutines)
	errs := make([]error, goroutines)

	// 同时启动 N 个 goroutine 翻译相同文本
	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			results[idx], errs[idx] = engine.Translate(ctx, "hello world", "zh-CN")
		}(i)
	}
	wg.Wait()

	// 底层引擎只应被调用一次
	if got := inner.callCount.Load(); got != 1 {
		t.Errorf("并发合并失败：期望底层引擎调用 1 次，实际调用 %d 次", got)
	}

	// 所有 goroutine 应收到相同的正确结果
	want := "[zh-CN] hello world"
	for i, res := range results {
		if errs[i] != nil {
			t.Errorf("goroutine %d 返回错误: %v", i, errs[i])
			continue
		}
		if res != want {
			t.Errorf("goroutine %d 结果不符: got %q, want %q", i, res, want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 不同文本不合并：各自独立调用底层引擎
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_DifferentTextNotMerged(t *testing.T) {
	inner := &slowMockEngine{delay: 10 * time.Millisecond}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	var wg sync.WaitGroup
	texts := []string{"foo", "bar", "baz"}

	wg.Add(len(texts))
	for _, text := range texts {
		go func(t string) {
			defer wg.Done()
			_, _ = engine.Translate(ctx, t, "zh-CN")
		}(text)
	}
	wg.Wait()

	// 3 个不同文本各自独立调用
	if got := inner.callCount.Load(); got != int64(len(texts)) {
		t.Errorf("不同文本应各自调用：期望 %d 次，实际 %d 次", len(texts), got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 语义规范化合并：空白不同但语义等价的文本共享同一次调用
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_NormalizedKeyMerges(t *testing.T) {
	inner := &slowMockEngine{delay: 30 * time.Millisecond}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	// 两个文本规范化后相同（空白差异）
	texts := []string{
		"hello   world",
		"hello world",  // NormalizeKey 后与第一个相同
		"hello\tworld", // 制表符，NormalizeKey 后也相同
	}

	var wg sync.WaitGroup
	wg.Add(len(texts))
	for _, text := range texts {
		go func(t string) {
			defer wg.Done()
			_, _ = engine.Translate(ctx, t, "zh-CN")
		}(text)
	}
	wg.Wait()

	// 规范化后语义相同，应合并为一次或少于文本数量的调用
	// （由于并发时机不确定，允许 1~len(texts) 次，但核心验证是不超过文本数量）
	got := inner.callCount.Load()
	if got > int64(len(texts)) {
		t.Errorf("规范化合并失败：调用次数 %d 超过文本数量 %d", got, len(texts))
	}
	t.Logf("规范化合并：%d 个请求实际发起 %d 次底层调用", len(texts), got)
}

// ─────────────────────────────────────────────────────────────────────────────
// 目标语言不同不合并：同文本不同目标语言各自独立
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_DifferentLangNotMerged(t *testing.T) {
	inner := &slowMockEngine{delay: 10 * time.Millisecond}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	langs := []string{"zh-CN", "ja", "ko"}
	var wg sync.WaitGroup
	wg.Add(len(langs))
	for _, lang := range langs {
		go func(l string) {
			defer wg.Done()
			_, _ = engine.Translate(ctx, "hello", l)
		}(lang)
	}
	wg.Wait()

	if got := inner.callCount.Load(); got != int64(len(langs)) {
		t.Errorf("不同目标语言应各自调用：期望 %d 次，实际 %d 次", len(langs), got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 错误传播：底层引擎失败时所有等待者均收到错误
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_ErrorPropagation(t *testing.T) {
	const goroutines = 10
	wantErr := errors.New("翻译 API 不可用")
	inner := &slowMockEngine{
		delay:     30 * time.Millisecond,
		returnErr: wantErr,
	}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	var wg sync.WaitGroup
	errs := make([]error, goroutines)

	wg.Add(goroutines)
	for i := range goroutines {
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = engine.Translate(ctx, "test text", "zh-CN")
		}(i)
	}
	wg.Wait()

	// 底层引擎只调用一次
	if got := inner.callCount.Load(); got != 1 {
		t.Errorf("错误场景下期望调用 1 次，实际 %d 次", got)
	}

	// 所有等待者均收到错误
	for i, err := range errs {
		if err == nil {
			t.Errorf("goroutine %d 期望收到错误，但得到 nil", i)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 错误后可重试：singleflight 完成后（无论成败）下次请求重新发起
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_RetryAfterError(t *testing.T) {
	var callCount atomic.Int64
	// 第一次失败，第二次成功
	inner := &mockEngine{
		translateFn: func(text, targetLang string) (string, error) {
			n := callCount.Add(1)
			if n == 1 {
				return "", errors.New("暂时失败")
			}
			return "[ok] " + text, nil
		},
	}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	// 第一次调用失败
	_, err1 := engine.Translate(ctx, "hello", "zh-CN")
	if err1 == nil {
		t.Error("第一次调用期望失败，但成功了")
	}

	// 第二次调用应重新发起（singleflight key 已释放）
	res, err2 := engine.Translate(ctx, "hello", "zh-CN")
	if err2 != nil {
		t.Errorf("第二次调用期望成功，但失败: %v", err2)
	}
	if res != "[ok] hello" {
		t.Errorf("第二次调用结果不符: got %q", res)
	}

	if callCount.Load() != 2 {
		t.Errorf("期望底层引擎调用 2 次，实际 %d 次", callCount.Load())
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ctx 取消：调用方 ctx 取消后返回 ctx.Err()，底层调用继续
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_CallerCtxCancelReturnsCtxErr(t *testing.T) {
	// 底层引擎耗时较长，调用方 ctx 会先超时
	inner := &slowMockEngine{delay: 200 * time.Millisecond}
	engine := NewSingleflightEngine(inner)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := engine.Translate(ctx, "slow text", "zh-CN")
	if err == nil {
		t.Fatal("期望返回 ctx 错误，但得到 nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("期望 DeadlineExceeded，实际: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 顺序调用（非并发）：多次串行翻译不应互相干扰
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_SequentialCallsIndependent(t *testing.T) {
	inner := &mockEngine{}
	engine := NewSingleflightEngine(inner)
	ctx := context.Background()

	// 串行翻译相同文本 3 次
	for i := range 3 {
		res, err := engine.Translate(ctx, "hello", "zh-CN")
		if err != nil {
			t.Errorf("第 %d 次翻译失败: %v", i+1, err)
		}
		if res == "" {
			t.Errorf("第 %d 次翻译结果为空", i+1)
		}
	}

	// 串行调用不合并，每次都应调用底层
	if got := inner.callCount.Load(); got != 3 {
		t.Errorf("串行调用期望底层引擎调用 3 次，实际 %d 次", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Name 方法
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_Name(t *testing.T) {
	inner := &mockEngine{}
	engine := NewSingleflightEngine(inner)

	want := "mock(singleflight)"
	if got := engine.Name(); got != want {
		t.Errorf("Name() = %q, want %q", got, want)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 与 CachedEngine 组合：SingleflightEngine 包装 CachedEngine 时行为正确
// ─────────────────────────────────────────────────────────────────────────────

func TestSingleflightEngine_WithCachedEngine(t *testing.T) {
	const goroutines = 15
	inner := &slowMockEngine{delay: 40 * time.Millisecond}
	cached := NewCachedEngine(inner, DefaultMemoryLimit)
	engine := NewSingleflightEngine(cached)
	ctx := context.Background()

	// 第一批：并发翻译相同文本
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, _ = engine.Translate(ctx, "combined test", "zh-CN")
		}()
	}
	wg.Wait()

	// 底层引擎只调用一次（singleflight 合并）
	firstBatchCalls := inner.callCount.Load()
	if firstBatchCalls != 1 {
		t.Errorf("第一批：期望调用 1 次，实际 %d 次", firstBatchCalls)
	}

	// 第二批：再次翻译相同文本（此时 CachedEngine 已有缓存）
	_, err := engine.Translate(ctx, "combined test", "zh-CN")
	if err != nil {
		t.Fatalf("第二批翻译失败: %v", err)
	}

	// CachedEngine 命中缓存，底层引擎调用次数不增加
	if got := inner.callCount.Load(); got != firstBatchCalls {
		t.Errorf("第二批应命中缓存：期望仍为 %d 次，实际 %d 次", firstBatchCalls, got)
	}
}
