package translate

import (
	"container/list"
	"context"
	"strings"
	"sync"
)

// Engine 是翻译引擎接口，所有翻译后端均需实现此接口
type Engine interface {
	// Translate 将 text 翻译为 targetLang 所指定的语言
	Translate(ctx context.Context, text, targetLang string) (string, error)
	// Name 返回引擎名称，用于日志和调试
	Name() string
}

// cacheKey 是 LRU 缓存的键，由规范化后的原文和目标语言共同构成。
// text 字段存储的是经过 [NormalizeKey] 处理后的文本，确保语义等价的文本共享缓存。
type cacheKey struct {
	text       string
	targetLang string
}

// NormalizeKey 对文本进行语义规范化，用于生成缓存键。
//
// 规则：
//  1. 将所有连续空白字符（空格、制表符）折叠为单个空格
//  2. 统一换行风格（\r\n → \n）
//  3. 去除每行的行尾空白
//  4. 折叠三个及以上的连续空行为两个空行（保留段落分隔语义）
//  5. 去除首尾空白
//
// 这使得仅空白差异的文本（如不同编辑器换行风格、缩进差异）命中同一缓存条目。
func NormalizeKey(text string) string {
	// 统一换行风格
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// 折叠行内连续空白为单个空格，并去除行首尾空白
		lines[i] = strings.Join(strings.Fields(line), " ")
	}

	// 折叠连续空行：最多保留一个空行（两个 \n）
	var result strings.Builder
	result.Grow(len(text))
	emptyCount := 0
	for i, line := range lines {
		if line == "" {
			emptyCount++
			if emptyCount <= 1 {
				if i > 0 {
					result.WriteByte('\n')
				}
			}
			continue
		}
		if emptyCount > 0 && result.Len() > 0 && emptyCount > 1 {
			// 已经写了一个空行（上面 emptyCount<=1 的分支），多余的不写
		}
		emptyCount = 0
		if result.Len() > 0 {
			result.WriteByte('\n')
		}
		result.WriteString(line)
	}

	return result.String()
}

// cacheEntry 是存储在双向链表节点中的缓存条目
type cacheEntry struct {
	key      cacheKey
	value    string
	byteSize int64 // 该条目占用的估算字节数
}

const (
	// DefaultMemoryLimit 默认内存缓存上限：30MB
	DefaultMemoryLimit int64 = 30 * 1024 * 1024
)

// CachedEngine 是带 LRU 缓存的翻译引擎包装器。
// 缓存上限由内存字节数控制（默认 30MB），而非条目数量。
// 相同的原文 + 目标语言组合命中缓存时，直接返回缓存结果，不再调用底层引擎。
type CachedEngine struct {
	engine      Engine
	memoryLimit int64 // 内存缓存字节上限

	mu          sync.Mutex
	items       map[cacheKey]*list.Element // 键 → 链表节点
	order       *list.List                 // 双向链表，头部为最近使用，尾部为最久未使用
	currentSize int64                      // 当前缓存占用字节数
}

// NewCachedEngine 创建一个带 LRU 缓存的翻译引擎包装器。
// memoryLimit 为缓存最大字节数，<= 0 时使用默认值 30MB。
func NewCachedEngine(engine Engine, memoryLimit int64) *CachedEngine {
	if memoryLimit <= 0 {
		memoryLimit = DefaultMemoryLimit
	}
	return &CachedEngine{
		engine:      engine,
		memoryLimit: memoryLimit,
		items:       make(map[cacheKey]*list.Element),
		order:       list.New(),
	}
}

// entrySize 估算一个缓存条目的字节占用（key + value 的字符串字节数 + 固定开销）
func entrySize(key cacheKey, value string) int64 {
	// 字符串本身的字节 + 结构体固定字段开销（约 64 字节）
	return int64(len(key.text)+len(key.targetLang)+len(value)) + 64
}

// Translate 先查询内存缓存，命中则直接返回；未命中则调用底层引擎并将结果写入内存缓存。
// 缓存 key 经过 [NormalizeKey] 规范化，使语义等价的文本共享缓存。
func (c *CachedEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	key := cacheKey{text: NormalizeKey(text), targetLang: targetLang}

	// --- 读取内存缓存（加锁）---
	c.mu.Lock()
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		result := elem.Value.(*cacheEntry).value
		c.mu.Unlock()
		return result, nil
	}
	c.mu.Unlock()

	// --- 内存缓存未命中：调用底层引擎（不持锁，避免阻塞其他 goroutine）---
	result, err := c.engine.Translate(ctx, text, targetLang)
	if err != nil {
		return "", err
	}

	// --- 写入内存缓存（加锁）---
	c.mu.Lock()
	defer c.mu.Unlock()

	// 二次检查：可能在释放锁期间已有其他 goroutine 写入了相同 key
	if elem, ok := c.items[key]; ok {
		c.order.MoveToFront(elem)
		return elem.Value.(*cacheEntry).value, nil
	}

	size := entrySize(key, result)

	// 若单条条目本身超过内存上限，则不缓存（直接返回结果）
	if size > c.memoryLimit {
		return result, nil
	}

	// 驱逐尾部条目直到腾出足够空间
	for c.currentSize+size > c.memoryLimit && c.order.Len() > 0 {
		c.evict()
	}

	// 将新条目插入链表头部
	entry := &cacheEntry{key: key, value: result, byteSize: size}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
	c.currentSize += size

	return result, nil
}

// Put 将外部获取的翻译结果（如磁盘词典命中）写入内存缓存。
// 若内存已满则 LRU 驱逐，若单条超限则不缓存。
// key 经过 [NormalizeKey] 规范化。
func (c *CachedEngine) Put(text, targetLang, value string) {
	key := cacheKey{text: NormalizeKey(text), targetLang: targetLang}
	size := entrySize(key, value)
	if size > c.memoryLimit {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.items[key]; ok {
		return // 已存在，无需重复写入
	}

	for c.currentSize+size > c.memoryLimit && c.order.Len() > 0 {
		c.evict()
	}

	entry := &cacheEntry{key: key, value: value, byteSize: size}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
	c.currentSize += size
}

// Name 返回底层引擎名称（带缓存标识）
func (c *CachedEngine) Name() string {
	return c.engine.Name() + "(cached)"
}

// Stats 返回当前内存缓存的统计信息（条目数、字节数）
func (c *CachedEngine) Stats() (count int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items), c.currentSize
}

// evict 驱逐链表尾部（最久未使用）的缓存条目。
// 调用方必须持有 c.mu 锁。
func (c *CachedEngine) evict() {
	tail := c.order.Back()
	if tail == nil {
		return
	}
	entry := tail.Value.(*cacheEntry)
	delete(c.items, entry.key)
	c.order.Remove(tail)
	c.currentSize -= entry.byteSize
	if c.currentSize < 0 {
		c.currentSize = 0
	}
}

// Close 关闭底层引擎（若实现了 io.Closer）。
func (c *CachedEngine) Close() error {
	if closer, ok := c.engine.(interface{ Close() error }); ok {
		return closer.Close()
	}
	return nil
}
