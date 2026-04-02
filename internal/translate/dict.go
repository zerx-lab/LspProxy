package translate

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// dictKey 是磁盘词典的键，格式为 "targetLang\x00text"
// 使用 JSON 序列化时直接用复合字符串以保持兼容性
type dictRecord struct {
	Text       string `json:"t"`
	TargetLang string `json:"l"`
	Value      string `json:"v"`
}

// diskDictData 是 JSON 文件中存储的数据结构
// key = targetLang + "\x00" + text，value = 翻译结果
type diskDictData = map[string]string

// DiskDict 是基于 JSON 文件的持久化翻译词典。
// 启动时将整个词典加载到内存索引，查询时直接走内存，
// 每次新增翻译后异步写盘（防止频繁 I/O）。
type DiskDict struct {
	path string

	mu      sync.RWMutex
	data    diskDictData  // 内存索引：key → 翻译值
	dirty   bool          // 是否有未写盘的变更
	writeCh chan struct{} // 写盘信号
	stopCh  chan struct{} // 停止信号
}

// NewDiskDict 创建并加载磁盘词典。
// path 为词典文件路径，文件不存在时自动创建。
func NewDiskDict(path string) (*DiskDict, error) {
	d := &DiskDict{
		path:    path,
		data:    make(diskDictData),
		writeCh: make(chan struct{}, 1),
		stopCh:  make(chan struct{}),
	}

	if err := d.load(); err != nil {
		return nil, err
	}

	// 启动后台写盘 goroutine
	go d.writeLoop()

	return d, nil
}

// dictKey 生成词典键
func dictKey(targetLang, text string) string {
	return targetLang + "\x00" + text
}

// Get 从磁盘词典查询翻译结果。未命中返回 ("", false)。
func (d *DiskDict) Get(text, targetLang string) (string, bool) {
	d.mu.RLock()
	v, ok := d.data[dictKey(targetLang, text)]
	d.mu.RUnlock()
	return v, ok
}

// Put 将翻译结果写入磁盘词典内存索引，并异步触发写盘。
func (d *DiskDict) Put(text, targetLang, value string) {
	key := dictKey(targetLang, text)

	d.mu.Lock()
	if existing, ok := d.data[key]; ok && existing == value {
		d.mu.Unlock()
		return // 已存在且相同，无需写盘
	}
	d.data[key] = value
	d.dirty = true
	d.mu.Unlock()

	// 非阻塞发送写盘信号（channel 有缓冲，避免重复写盘）
	select {
	case d.writeCh <- struct{}{}:
	default:
	}
}

// Len 返回词典中的条目数
func (d *DiskDict) Len() int {
	d.mu.RLock()
	n := len(d.data)
	d.mu.RUnlock()
	return n
}

// Close 停止后台写盘 goroutine 并做最后一次写盘
func (d *DiskDict) Close() error {
	close(d.stopCh)
	return d.flush()
}

// load 从磁盘加载词典数据
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
	d.data = raw
	d.mu.Unlock()
	return nil
}

// flush 将内存词典同步写入磁盘（原子写：先写临时文件再重命名）
func (d *DiskDict) flush() error {
	d.mu.RLock()
	if !d.dirty {
		d.mu.RUnlock()
		return nil
	}
	// 复制一份数据，避免长时间持锁
	snapshot := make(diskDictData, len(d.data))
	for k, v := range d.data {
		snapshot[k] = v
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

	d.mu.Lock()
	d.dirty = false
	d.mu.Unlock()
	return nil
}

// writeLoop 后台写盘循环，收到写盘信号时执行 flush
func (d *DiskDict) writeLoop() {
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
func (d *DictEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	key := cacheKey{text: text, targetLang: targetLang}

	// 1. 查内存 LRU
	d.mem.mu.Lock()
	if elem, ok := d.mem.items[key]; ok {
		d.mem.order.MoveToFront(elem)
		result := elem.Value.(*cacheEntry).value
		d.mem.mu.Unlock()
		return result, nil
	}
	d.mem.mu.Unlock()

	// 2. 查磁盘词典
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

// Name 返回引擎名称
func (d *DictEngine) Name() string {
	return d.mem.engine.Name() + "(dict+cached)"
}
