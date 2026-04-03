package translate

import (
	"container/list"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// diskEntry 是磁盘词典链表节点中存储的条目
type diskEntry struct {
	key   string // dictKey(targetLang, text)
	value string // 翻译结果
}

// DiskDict 是基于 JSON 文件的持久化翻译词典，内置访问顺序 LRU 驱逐。
//
// 数据组织：
//   - items：key → *list.Element，用于 O(1) 查找
//   - order：双向链表，头部为最近访问，尾部为最久未访问
//
// 超出 maxEntries 时驱逐链表尾部（最久未访问）的条目。
// 启动时将整个词典加载到内存索引，查询时直接走内存，
// 每次变更后异步触发写盘（防止频繁 I/O）。
type DiskDict struct {
	path       string
	maxEntries int // 最大条目数，0 表示不限制

	mu         sync.RWMutex
	items      map[string]*list.Element // key → 链表节点
	order      *list.List               // 头部=最近访问，尾部=最久未访问
	generation uint64                   // 数据变更的代数计数器，每次 Put/evict 自增
	flushedGen uint64                   // 已成功写盘的代数
	writeCh    chan struct{}            // 写盘信号
	stopCh     chan struct{}            // 停止信号
	done       chan struct{}            // writeLoop 退出信号
}

// NewDiskDict 创建并加载磁盘词典。
// path 为词典文件路径，文件不存在时自动创建。
// maxEntries 为最大条目数，<= 0 时不限制容量。
func NewDiskDict(path string, maxEntries int) (*DiskDict, error) {
	d := &DiskDict{
		path:       path,
		maxEntries: maxEntries,
		items:      make(map[string]*list.Element),
		order:      list.New(),
		writeCh:    make(chan struct{}, 1),
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
	}

	if err := d.load(); err != nil {
		return nil, err
	}

	// 启动后台写盘 goroutine
	go d.writeLoop()

	return d, nil
}

// dictKey 生成词典键，text 经过 [NormalizeKey] 规范化以提高缓存复用率。
func dictKey(targetLang, text string) string {
	return targetLang + "\x00" + NormalizeKey(text)
}

// Get 从磁盘词典查询翻译结果，命中时将条目移至链表头部（更新访问顺序）。
// 未命中返回 ("", false)。
func (d *DiskDict) Get(text, targetLang string) (string, bool) {
	key := dictKey(targetLang, text)

	d.mu.Lock()
	elem, ok := d.items[key]
	if !ok {
		d.mu.Unlock()
		return "", false
	}
	// 命中：移至链表头部，更新访问顺序
	d.order.MoveToFront(elem)
	value := elem.Value.(*diskEntry).value
	d.mu.Unlock()

	return value, true
}

// Put 将翻译结果写入磁盘词典，并异步触发写盘。
// 若 key 已存在则更新值并移至链表头部；
// 若超出 maxEntries 则驱逐尾部（最久未访问）条目后再插入。
func (d *DiskDict) Put(text, targetLang, value string) {
	key := dictKey(targetLang, text)
	changed := false

	d.mu.Lock()

	if elem, ok := d.items[key]; ok {
		entry := elem.Value.(*diskEntry)
		if entry.value == value {
			// 值未变化：仅更新访问顺序，不触发写盘
			d.order.MoveToFront(elem)
			d.mu.Unlock()
			return
		}
		// 值有变化：更新并移至头部
		entry.value = value
		d.order.MoveToFront(elem)
		d.generation++
		changed = true
	} else {
		// 新条目：超出上限时先驱逐
		if d.maxEntries > 0 {
			for d.order.Len() >= d.maxEntries {
				d.evictLocked()
			}
		}
		entry := &diskEntry{key: key, value: value}
		elem := d.order.PushFront(entry)
		d.items[key] = elem
		d.generation++
		changed = true
	}

	d.mu.Unlock()

	if changed {
		// 非阻塞发送写盘信号（channel 有缓冲，避免重复写盘）
		select {
		case d.writeCh <- struct{}{}:
		default:
		}
	}
}

// Len 返回词典中的条目数
func (d *DiskDict) Len() int {
	d.mu.RLock()
	n := len(d.items)
	d.mu.RUnlock()
	return n
}

// Close 停止后台写盘 goroutine 并做最后一次写盘。
// 先等待 writeLoop 完全退出，再执行最终 flush，避免并发写盘竞争。
func (d *DiskDict) Close() error {
	close(d.stopCh)
	<-d.done // 等待 writeLoop 退出
	return d.flush()
}

// evictLocked 驱逐链表尾部（最久未访问）的条目。
// 调用方必须持有 d.mu 写锁。
func (d *DiskDict) evictLocked() {
	tail := d.order.Back()
	if tail == nil {
		return
	}
	entry := tail.Value.(*diskEntry)
	delete(d.items, entry.key)
	d.order.Remove(tail)
	d.generation++ // 驱逐也是数据变更，需要写盘
}

// load 从磁盘加载词典数据，并重建内存链表（按 JSON 迭代顺序，作为初始访问顺序）。
// 若加载后条目数超过 maxEntries，则截断至上限（保留链表靠前的条目）。
func (d *DiskDict) load() error {
	// 确保父目录存在
	dir := filepath.Dir(d.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.Open(d.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 文件不存在，使用空词典
		}
		return err
	}
	defer f.Close()

	var raw map[string]string
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		// 文件损坏时使用空词典，不返回错误（防止代理无法启动）
		return nil
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	// 重建链表（JSON map 迭代顺序不确定，视为随机初始访问顺序）
	for k, v := range raw {
		entry := &diskEntry{key: k, value: v}
		elem := d.order.PushBack(entry) // PushBack：旧条目视为"较久访问"，新请求会升到头部
		d.items[k] = elem
	}

	// 若加载的条目数超过上限，驱逐链表尾部（随机淘汰旧条目）
	if d.maxEntries > 0 {
		for d.order.Len() > d.maxEntries {
			d.evictLocked()
		}
	}

	return nil
}

// flush 将内存词典同步写入磁盘（原子写：先写临时文件再重命名）。
// 使用 generation 计数器避免 TOCTOU 竞态：flush 开始时记录当前 generation，
// 写盘完成后仅当 generation 未被其他 goroutine 推进时才更新 flushedGen。
func (d *DiskDict) flush() error {
	d.mu.RLock()
	gen := d.generation
	if gen == d.flushedGen {
		d.mu.RUnlock()
		return nil // 没有未写盘的变更
	}
	// 按链表顺序构建快照（头部=最近访问）
	snapshot := make(map[string]string, d.order.Len())
	for elem := d.order.Front(); elem != nil; elem = elem.Next() {
		entry := elem.Value.(*diskEntry)
		snapshot[entry.key] = entry.value
	}
	d.mu.RUnlock()

	// 序列化
	b, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}

	// 写入临时文件
	tmpPath := d.path + ".tmp"
	if err := os.WriteFile(tmpPath, b, 0o644); err != nil {
		return err
	}

	// 原子重命名
	if err := os.Rename(tmpPath, d.path); err != nil {
		return err
	}

	// 只有当 generation 没有被其他 goroutine 推进时才更新 flushedGen，
	// 否则说明在写盘期间有新数据写入，需要下次 flush 再处理。
	d.mu.Lock()
	if d.generation == gen {
		d.flushedGen = gen
	}
	d.mu.Unlock()
	return nil
}

// writeLoop 后台写盘循环，收到写盘信号时执行 flush。
// 退出时关闭 done channel 通知 Close()。
func (d *DiskDict) writeLoop() {
	defer close(d.done)
	for {
		select {
		case <-d.writeCh:
			_ = d.flush()
		case <-d.stopCh:
			return
		}
	}
}

// DictEngine 是三级缓存翻译引擎：
//
//	内存 LRU → 磁盘词典 → 底层在线翻译引擎
type DictEngine struct {
	mem  *CachedEngine
	disk *DiskDict
}

// NewDictEngine 创建三级缓存翻译引擎。
func NewDictEngine(base Engine, memoryLimit int64, disk *DiskDict) *DictEngine {
	return &DictEngine{
		mem:  NewCachedEngine(base, memoryLimit),
		disk: disk,
	}
}

// Translate 按 内存 → 磁盘词典 → 在线翻译 的顺序查询。
// 所有缓存 key 经过 [NormalizeKey] 规范化，使语义等价的文本共享缓存。
func (d *DictEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	key := cacheKey{text: NormalizeKey(text), targetLang: targetLang}

	// 1. 查内存 LRU
	d.mem.mu.Lock()
	if elem, ok := d.mem.items[key]; ok {
		d.mem.order.MoveToFront(elem)
		result := elem.Value.(*cacheEntry).value
		d.mem.mu.Unlock()
		return result, nil
	}
	d.mem.mu.Unlock()

	// 2. 查磁盘词典（命中时内部已更新访问顺序）
	if v, ok := d.disk.Get(text, targetLang); ok {
		// 命中磁盘词典，回填内存缓存
		d.mem.Put(text, targetLang, v)
		return v, nil
	}

	// 3. 在线翻译
	result, err := d.mem.engine.Translate(ctx, text, targetLang)
	if err != nil {
		return "", err
	}

	// 写入磁盘词典（持久化）
	d.disk.Put(text, targetLang, result)
	// 写入内存缓存
	d.mem.Put(text, targetLang, result)

	return result, nil
}

// Close 关闭磁盘词典，停止后台写盘并做最终持久化。
func (d *DictEngine) Close() error {
	return d.disk.Close()
}

// Name 返回引擎名称
func (d *DictEngine) Name() string {
	return d.mem.engine.Name() + "(dict+cached)"
}
