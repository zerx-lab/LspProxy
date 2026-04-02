'use client'

const features = [
  {
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" />
      </svg>
    ),
    title: '实时透明代理',
    desc: '无缝插入编辑器与 LSP 之间，零感知延迟，编辑器无需任何配置变更。',
  },
  {
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <path d="M3 9h18M9 21V9" />
      </svg>
    ),
    title: 'Markdown 智能分割',
    desc: '自动识别代码块与文本，只翻译文档文字，代码原文保留，保证专业准确。',
  },
  {
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <circle cx="12" cy="12" r="10" />
        <path d="M12 2a14.5 14.5 0 0 0 0 20 14.5 14.5 0 0 0 0-20" />
        <path d="M2 12h20" />
      </svg>
    ),
    title: '多翻译引擎',
    desc: '内置 Google 免费翻译（无需密钥），支持 OpenAI/DeepSeek/Qwen/Ollama 等兼容 API。',
  },
  {
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <path d="M20 7H4a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2z" />
        <path d="M9 12h.01M15 12h.01" />
      </svg>
    ),
    title: '多编辑器支持',
    desc: '完整支持 VSCode、Neovim、Zed，标准 LSP stdio 协议，任何支持 LSP 的编辑器均可使用。',
  },
  {
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z" />
      </svg>
    ),
    title: '全消息覆盖',
    desc: '翻译 hover、completion、diagnostics、signatureHelp 全部 LSP 消息类型，不遗漏任何文档。',
  },
  {
    icon: (
      <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
        <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
        <circle cx="9" cy="7" r="4" />
        <path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75" />
      </svg>
    ),
    title: '零运行时依赖',
    desc: '纯 Go 实现，单二进制可执行文件，无需任何运行时环境，跨平台支持 Linux / macOS / Windows。',
  },
]

export function FeaturesSection() {
  return (
    <section
      className="relative py-20 px-6 sm:px-10 transition-colors duration-300"
      style={{ background: 'var(--bg-base)', borderTop: '1px solid var(--border)' }}
    >
      <div className="max-w-6xl mx-auto">
        {/* Header */}
        <div className="mb-12">
          <p className="font-mono text-xs uppercase tracking-widest mb-3 transition-colors duration-300"
            style={{ color: 'var(--fg-faint)' }}>特性</p>
          <h2 className="text-2xl sm:text-3xl font-medium tracking-tight max-w-lg transition-colors duration-300"
            style={{ color: 'var(--fg-strong)' }}>
            为中文开发者打造的{' '}
            <span style={{ color: 'var(--fg-faint)' }}>LSP 语言服务层</span>
          </h2>
        </div>

        {/* Grid */}
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3"
          style={{ border: '1px solid var(--border)' }}>
          {features.map((f, idx) => (
            <div
              key={f.title}
              className="p-6 transition-colors duration-200 group cursor-default"
              style={{
                borderRight: idx % 3 !== 2 ? '1px solid var(--border)' : 'none',
                borderBottom: idx < 3 ? '1px solid var(--border)' : 'none',
              }}
              onMouseEnter={e => ((e.currentTarget as HTMLElement).style.background = 'var(--bg-surface)')}
              onMouseLeave={e => ((e.currentTarget as HTMLElement).style.background = 'transparent')}
            >
              <div className="mb-4 transition-colors duration-200"
                style={{ color: 'var(--fg-faint)' }}>
                {f.icon}
              </div>
              <h3 className="text-sm font-medium mb-2 transition-colors duration-300"
                style={{ color: 'var(--fg-base)' }}>{f.title}</h3>
              <p className="text-xs leading-relaxed transition-colors duration-300"
                style={{ color: 'var(--fg-muted)' }}>{f.desc}</p>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
