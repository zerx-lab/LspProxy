'use client'

import Link from 'next/link'

export function CtaSection() {
  return (
    <section
      className="relative py-20 px-6 sm:px-10 overflow-hidden transition-colors duration-300"
      style={{ background: 'var(--bg-base)', borderTop: '1px solid var(--border)' }}
    >
      {/* Glow */}
      <div className="absolute inset-0 pointer-events-none"
        style={{ background: 'radial-gradient(ellipse 60% 60% at 50% 100%, var(--glow-top) 0%, transparent 70%)' }} />

      <div className="relative max-w-4xl mx-auto text-center">
        <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full mb-6 transition-colors duration-300"
          style={{ border: '1px solid var(--border)', background: 'var(--bg-surface)' }}>
          <span className="text-xs font-mono transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>
            开源 · MIT 协议
          </span>
        </div>

        <h2 className="text-2xl sm:text-3xl xl:text-4xl font-medium tracking-tight mb-4 text-balance transition-colors duration-300"
          style={{ color: 'var(--fg-strong)' }}>
          从今天起，用母语理解代码
        </h2>
        <p className="text-sm max-w-sm mx-auto mb-8 transition-colors duration-300" style={{ color: 'var(--fg-muted)' }}>
          让语言不再成为阅读代码文档的障碍。LspProxy 让每位中文开发者都能流畅理解英文 LSP 文档。
        </p>

        <div className="flex flex-wrap items-center justify-center gap-3">
          <Link href="/docs/installation"
            className="inline-flex items-center gap-2 px-6 py-3 text-sm font-medium transition-colors duration-150"
            style={{ background: 'var(--fg-strong)', color: 'var(--bg-base)' }}>
            快速开始
            <svg className="w-3 h-3 opacity-50" viewBox="0 0 10 10" fill="none">
              <path d="M1 9L9 1M9 1H3M9 1V7" stroke="currentColor" strokeWidth="1.2" />
            </svg>
          </Link>
          <a href="https://github.com/zerx-lab/LspProxy"
            className="inline-flex items-center gap-2 px-6 py-3 text-sm transition-colors duration-150"
            style={{ border: '1px solid var(--border)', color: 'var(--fg-muted)' }}
            onMouseEnter={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-base)')}
            onMouseLeave={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-muted)')}>
            <svg className="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0022 12.017C22 6.484 17.522 2 12 2z" />
            </svg>
            查看源码
          </a>
        </div>
      </div>

      {/* Footer */}
      <div className="relative mt-20 pt-8 flex flex-col sm:flex-row items-center justify-between gap-4 transition-colors duration-300"
        style={{ borderTop: '1px solid var(--border-subtle)' }}>
        <div className="flex items-center gap-2">
          <svg width="20" height="16" viewBox="0 0 60 45" fill="none">
            <path fillRule="evenodd" clipRule="evenodd"
              d="M0 0H15V15H30V30H15V45H0V30V15V0ZM45 30V15H30V0H45H60V15V30V45H45H30V30H45Z"
              style={{ fill: 'var(--fg-faint)' }} />
          </svg>
          <span className="font-mono text-xs uppercase tracking-widest select-none transition-colors duration-300"
            style={{ color: 'var(--fg-faint)' }}>
            LSP-PROXY.
          </span>
        </div>
        <p className="text-xs font-mono transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>
          MIT © 2025 zerx-lab
        </p>
        <div className="flex items-center gap-4">
          {[['文档', '/docs'], ['GitHub', 'https://github.com/zerx-lab/LspProxy']].map(([label, href]) => (
            <a key={label} href={href}
              className="text-xs font-mono transition-colors duration-150"
              style={{ color: 'var(--fg-faint)' }}
              onMouseEnter={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-muted)')}
              onMouseLeave={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-faint)')}>
              {label}
            </a>
          ))}
        </div>
      </div>
    </section>
  )
}
