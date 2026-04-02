'use client'

function lineColor(line: string): string {
  if (line.startsWith('//') || line.startsWith('--')) return 'var(--code-comment)'
  if (line.includes("'") || line.includes('"')) return 'var(--code-string)'
  return 'var(--code-default)'
}

const steps = [
  {
    num: '01',
    title: '下载二进制',
    code: 'go install github.com/zerx-lab/LspProxy@latest',
    desc: '使用 go install 一键安装，或从 GitHub Releases 下载预编译二进制。',
  },
  {
    num: '02',
    title: '修改编辑器配置',
    code: `-- Neovim (lua)\nrequire('lspconfig').rust_analyzer.setup({\n  cmd = { 'lsp-proxy', '--', 'rust-analyzer' },\n})`,
    desc: '将原始 LSP 命令包装在 lsp-proxy 中，传入目标 LSP 命令作为参数。',
  },
  {
    num: '03',
    title: '享受中文文档',
    code: '// hover 文档自动翻译为中文\n// diagnostics 错误信息也全部中文',
    desc: '无需重启，即时生效。所有 LSP 文档从此以中文呈现。',
  },
]

export function HowItWorksSection() {
  return (
    <section
      className="relative py-20 px-6 sm:px-10 transition-colors duration-300"
      style={{ background: 'var(--bg-base)', borderTop: '1px solid var(--border)' }}
    >
      <div className="max-w-6xl mx-auto">
        <div className="mb-12">
          <p className="font-mono text-xs uppercase tracking-widest mb-3 transition-colors duration-300"
            style={{ color: 'var(--fg-faint)' }}>工作原理</p>
          <h2 className="text-2xl sm:text-3xl font-medium tracking-tight transition-colors duration-300"
            style={{ color: 'var(--fg-strong)' }}>
            三步启动，立即使用
          </h2>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-3 gap-5">
          {steps.map((step, i) => (
            <div key={step.num} className="relative">
              {/* Connector */}
              {i < steps.length - 1 && (
                <div className="hidden lg:block absolute top-[3.25rem] right-0 w-5 h-px translate-x-full transition-colors duration-300"
                  style={{ background: 'var(--border)' }} />
              )}

              <div className="overflow-hidden rounded-lg transition-colors duration-300"
                style={{ border: '1px solid var(--border)', background: 'var(--bg-surface)' }}>
                {/* Header */}
                <div className="flex items-center gap-3 px-5 py-4 transition-colors duration-300"
                  style={{ borderBottom: '1px solid var(--border)' }}>
                  <span className="font-mono text-xs transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>
                    {step.num}
                  </span>
                  <h3 className="text-sm font-medium transition-colors duration-300" style={{ color: 'var(--fg-base)' }}>
                    {step.title}
                  </h3>
                </div>

                {/* Code */}
                <div className="p-4 transition-colors duration-300" style={{ background: 'var(--bg-code)' }}>
                  <pre className="text-xs leading-relaxed overflow-x-auto code-scrollbar" style={{ fontFamily: 'monospace' }}>
                    {step.code.split('\n').map((line, j) => (
                      <div key={j} style={{ display: 'flex', gap: '0.75rem' }}>
                        <span style={{ color: 'var(--code-linenum)', minWidth: '1rem', textAlign: 'right', userSelect: 'none', flexShrink: 0 }}>
                          {j + 1}
                        </span>
                        <span style={{ color: lineColor(line) }}>{line}</span>
                      </div>
                    ))}
                  </pre>
                </div>

                {/* Desc */}
                <div className="px-5 py-4">
                  <p className="text-xs leading-relaxed transition-colors duration-300" style={{ color: 'var(--fg-muted)' }}>
                    {step.desc}
                  </p>
                </div>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
