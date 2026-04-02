'use client'

import { useEffect, useRef } from 'react'
import Link from 'next/link'
import { ThemeToggle } from './ThemeToggle'

// ── 语法高亮：从 CSS token 读颜色，SSR 安全 ──────
const CODE_COLORS = {
  comment:  'var(--code-comment)',
  keyword:  'var(--code-keyword)',
  string:   'var(--code-string)',
  default:  'var(--code-default)',
  lineNum:  'var(--code-linenum)',
}

const KEYWORDS = /\b(proxy|lspproxy|Config|Google|OpenAI|OpenAIConfig|New|Run|Fatal|log|os|err|if|nil)\b/g

type Token = { text: string; color: string }

function tokenize(line: string): Token[] {
  if (/^\s*\/\//.test(line)) return [{ text: line, color: CODE_COLORS.comment }]
  const tokens: Token[] = []
  let rest = line
  while (rest.length > 0) {
    const ci = rest.indexOf('//')
    const sm = rest.match(/"[^"]*"/)
    const cp = ci === -1 ? Infinity : ci
    const sp = sm?.index ?? Infinity
    if (cp < sp) {
      if (cp > 0) tokens.push(...kwTokens(rest.slice(0, cp)))
      tokens.push({ text: rest.slice(cp), color: CODE_COLORS.comment })
      break
    } else if (sp < Infinity && sm) {
      if (sp > 0) tokens.push(...kwTokens(rest.slice(0, sp)))
      tokens.push({ text: sm[0], color: CODE_COLORS.string })
      rest = rest.slice(sp + sm[0].length)
    } else {
      tokens.push(...kwTokens(rest))
      break
    }
  }
  return tokens
}

function kwTokens(text: string): Token[] {
  const out: Token[] = []
  let last = 0
  const re = new RegExp(KEYWORDS.source, 'g')
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push({ text: text.slice(last, m.index), color: CODE_COLORS.default })
    out.push({ text: m[0], color: CODE_COLORS.keyword })
    last = m.index + m[0].length
  }
  if (last < text.length) out.push({ text: text.slice(last), color: CODE_COLORS.default })
  return out
}

function CodeLine({ lineNum, line }: { lineNum: number; line: string }) {
  return (
    <div style={{ display: 'flex', gap: '1rem' }}>
      <span style={{ color: CODE_COLORS.lineNum, minWidth: '1.25rem', textAlign: 'right', userSelect: 'none', flexShrink: 0 }}>
        {lineNum}
      </span>
      <span>
        {tokenize(line).map((t, i) => (
          <span key={i} style={{ color: t.color }}>{t.text}</span>
        ))}
      </span>
    </div>
  )
}

const CODE_SNIPPET = `// 配置 LspProxy
proxy := lspproxy.New(lspproxy.Config{
  Command:    "rust-analyzer",
  Translator: lspproxy.Google(),
  // 或使用 OpenAI 兼容 API
  // Translator: lspproxy.OpenAI(lspproxy.OpenAIConfig{
  //   Model:   "deepseek-chat",
  //   BaseURL: "https://api.deepseek.com/v1",
  //   APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
  // }),
})

// 启动代理 — 编辑器与 LSP 之间的桥梁
if err := proxy.Run(); err != nil {
  log.Fatal(err)
}`

export function HeroSection() {
  const canvasRef = useRef<HTMLCanvasElement>(null)

  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const ctx = canvas.getContext('2d')
    if (!ctx) return

    const draw = () => {
      canvas.width = canvas.offsetWidth
      canvas.height = canvas.offsetHeight
      ctx.clearRect(0, 0, canvas.width, canvas.height)
      // 读取 CSS 变量颜色
      const color = getComputedStyle(document.documentElement)
        .getPropertyValue('--grid-color').trim() || 'rgba(255,255,255,0.04)'
      ctx.strokeStyle = color
      ctx.lineWidth = 1
      const size = 40
      for (let x = 0; x < canvas.width; x += size) {
        ctx.beginPath(); ctx.moveTo(x, 0); ctx.lineTo(x, canvas.height); ctx.stroke()
      }
      for (let y = 0; y < canvas.height; y += size) {
        ctx.beginPath(); ctx.moveTo(0, y); ctx.lineTo(canvas.width, y); ctx.stroke()
      }
    }

    draw()
    window.addEventListener('resize', draw)

    // 监听 class 变化（dark/light 切换时重绘）
    const observer = new MutationObserver(draw)
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ['class'] })

    return () => {
      window.removeEventListener('resize', draw)
      observer.disconnect()
    }
  }, [])

  return (
    <section
      className="relative min-h-screen overflow-hidden transition-colors duration-300"
      style={{ background: 'var(--bg-base)' }}
    >
      {/* Grid canvas */}
      <canvas ref={canvasRef} className="absolute inset-0 w-full h-full pointer-events-none" />

      {/* Gradient glows */}
      <div className="absolute inset-0 pointer-events-none">
        <div className="absolute top-0 left-1/2 -translate-x-1/2 w-[800px] h-[400px]"
          style={{ background: 'radial-gradient(ellipse at center, var(--glow-top) 0%, transparent 70%)' }} />
        <div className="absolute top-1/3 left-0 w-[400px] h-[400px]"
          style={{ background: 'radial-gradient(ellipse at center, var(--glow-left) 0%, transparent 70%)' }} />
        <div className="absolute top-1/3 right-0 w-[400px] h-[400px]"
          style={{ background: 'radial-gradient(ellipse at center, var(--glow-right) 0%, transparent 70%)' }} />
      </div>

      {/* Navbar */}
      <nav
        className="relative z-10 flex items-center justify-between px-6 sm:px-8 h-14 transition-colors duration-300"
        style={{ borderBottom: '1px solid var(--border)' }}
      >
        <div className="flex items-center gap-2">
          <svg width="28" height="22" viewBox="0 0 60 45" fill="none">
            <path fillRule="evenodd" clipRule="evenodd"
              d="M0 0H15V15H30V30H15V45H0V30V15V0ZM45 30V15H30V0H45H60V15V30V45H45H30V30H45Z"
              style={{ fill: 'var(--fg-strong)' }} />
          </svg>
          <span className="font-mono text-sm uppercase tracking-widest select-none transition-colors duration-300"
            style={{ color: 'var(--fg-strong)' }}>
            LSP-PROXY.
          </span>
        </div>

        <div className="hidden md:flex items-center gap-6">
          {[['文档', '/docs'], ['安装', '/docs/installation'], ['GitHub', 'https://github.com/zerx-lab/LspProxy']].map(([label, href]) => (
            <a key={label} href={href}
              className="font-mono text-xs uppercase tracking-wider transition-colors duration-150"
              style={{ color: 'var(--fg-muted)' }}
              onMouseEnter={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-base)')}
              onMouseLeave={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-muted)')}>
              {label}
            </a>
          ))}
        </div>

        <div className="flex items-center gap-2">
          <ThemeToggle />
          <a href="https://github.com/zerx-lab/LspProxy"
            className="hidden sm:flex items-center gap-1.5 px-3 py-1.5 text-xs font-mono uppercase tracking-wider transition-colors duration-150"
            style={{ border: '1px solid var(--border)', color: 'var(--fg-muted)' }}
            onMouseEnter={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-base)')}
            onMouseLeave={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-muted)')}>
            <svg className="w-3.5 h-3.5" viewBox="0 0 24 24" fill="currentColor">
              <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0022 12.017C22 6.484 17.522 2 12 2z" />
            </svg>
            GitHub
          </a>
          <Link href="/docs/installation"
            className="flex items-center gap-1 px-4 py-1.5 text-xs font-mono uppercase tracking-wider transition-colors duration-150"
            style={{ background: 'var(--fg-strong)', color: 'var(--bg-base)' }}>
            快速开始
            <svg className="w-2.5 h-2.5 opacity-50" viewBox="0 0 10 10" fill="none">
              <path d="M1 9L9 1M9 1H3M9 1V7" stroke="currentColor" strokeWidth="1.2" />
            </svg>
          </Link>
        </div>
      </nav>

      {/* Hero body */}
      <div className="relative z-10 flex flex-col lg:flex-row min-h-[calc(100vh-56px)]">
        {/* Left */}
        <div className="flex-1 flex flex-col justify-center px-8 sm:px-12 lg:px-16 xl:px-24 py-16 lg:py-0 transition-colors duration-300"
          style={{ borderRight: '1px solid var(--border)' }}>

          {/* Badge */}
          <div className="inline-flex items-center gap-2 px-3 py-1.5 rounded-full w-fit mb-6 transition-colors duration-300"
            style={{ background: 'var(--bg-surface)', border: '1px solid var(--border)' }}>
            <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
            <span className="text-xs font-mono transition-colors duration-300" style={{ color: 'var(--fg-muted)' }}>
              v0.1 — 免费 · 开源
            </span>
          </div>

          <h1 className="text-3xl sm:text-4xl xl:text-5xl tracking-tight leading-tight text-balance font-medium mb-4 transition-colors duration-300"
            style={{ color: 'var(--fg-strong)' }}>
            LSP 中文翻译代理
            <br />
            <span style={{ color: 'var(--fg-faint)' }}>for 编辑器</span>
          </h1>

          <p className="text-sm sm:text-base leading-relaxed max-w-md mb-8 transition-colors duration-300"
            style={{ color: 'var(--fg-muted)' }}>
            透明插入编辑器与 LSP 之间，将 hover、completion、diagnostics
            等 LSP 消息实时翻译为中文。支持 VSCode、Neovim、Zed。
          </p>

          <div className="flex flex-wrap items-center gap-3">
            <Link href="/docs/installation"
              className="inline-flex items-center gap-2 px-5 py-2.5 text-sm font-medium transition-colors duration-150"
              style={{ background: 'var(--fg-strong)', color: 'var(--bg-base)' }}>
              开始使用
            </Link>
            <a href="https://github.com/zerx-lab/LspProxy"
              className="inline-flex items-center gap-2 px-5 py-2.5 text-sm transition-colors duration-150"
              style={{ border: '1px solid var(--border)', color: 'var(--fg-muted)' }}
              onMouseEnter={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-base)')}
              onMouseLeave={e => ((e.currentTarget as HTMLElement).style.color = 'var(--fg-muted)')}>
              <svg className="w-4 h-4" viewBox="0 0 24 24" fill="currentColor">
                <path d="M12 2C6.477 2 2 6.484 2 12.017c0 4.425 2.865 8.18 6.839 9.504.5.092.682-.217.682-.483 0-.237-.008-.868-.013-1.703-2.782.605-3.369-1.343-3.369-1.343-.454-1.158-1.11-1.466-1.11-1.466-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.53 2.341 1.088 2.91.832.092-.647.35-1.088.636-1.338-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.296 2.747-1.027 2.747-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.92.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.58.688.482A10.019 10.019 0 0022 12.017C22 6.484 17.522 2 12 2z" />
              </svg>
              在 GitHub 查看
            </a>
          </div>

          {/* Stats */}
          <div className="flex flex-wrap items-center gap-6 mt-10 pt-8 transition-colors duration-300"
            style={{ borderTop: '1px solid var(--border)' }}>
            {[
              { label: 'Go 实现', value: '零依赖' },
              { label: '翻译引擎', value: '2 种' },
              { label: '支持编辑器', value: '3+' },
            ].map(({ label, value }) => (
              <div key={label}>
                <div className="text-lg font-mono transition-colors duration-300" style={{ color: 'var(--fg-base)' }}>{value}</div>
                <div className="text-xs mt-0.5 transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>{label}</div>
              </div>
            ))}
          </div>
        </div>

        {/* Right: code window */}
        <div className="lg:w-[55%] flex items-center justify-center px-6 sm:px-10 py-12 lg:py-0">
          <div className="w-full max-w-2xl">
            {/* Window chrome */}
            <div className="rounded-lg overflow-hidden shadow-xl transition-colors duration-300"
              style={{ border: '1px solid var(--border)', background: 'var(--bg-code)' }}>
              {/* Title bar */}
              <div className="flex items-center gap-2 px-4 py-3 transition-colors duration-300"
                style={{ borderBottom: '1px solid var(--border)', background: 'var(--bg-code-hd)' }}>
                <div className="w-3 h-3 rounded-full bg-[#ff5f57]" />
                <div className="w-3 h-3 rounded-full bg-[#febc2e]" />
                <div className="w-3 h-3 rounded-full bg-[#28c840]" />
                <span className="ml-3 text-xs font-mono transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>
                  main.go
                </span>
              </div>
              {/* Code */}
              <pre className="p-5 overflow-x-auto text-xs sm:text-sm leading-relaxed code-scrollbar"
                style={{ fontFamily: 'monospace' }}>
                <code>
                  {CODE_SNIPPET.split('\n').map((line, i) => (
                    <CodeLine key={i} lineNum={i + 1} line={line} />
                  ))}
                </code>
              </pre>
            </div>

            {/* Translation preview */}
            <div className="mt-4 rounded-lg p-4 transition-colors duration-300"
              style={{ border: '1px solid var(--border)', background: 'var(--bg-surface)' }}>
              <div className="flex items-center gap-2 mb-3">
                <div className="w-1.5 h-1.5 rounded-full bg-emerald-500" />
                <span className="text-xs font-mono transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>
                  hover 翻译预览
                </span>
              </div>
              <div className="space-y-1.5">
                <div className="text-xs font-mono line-through transition-colors duration-300" style={{ color: 'var(--fg-faint)' }}>
                  Creates a new http.Handler that...
                </div>
                <div className="text-xs font-mono transition-colors duration-300" style={{ color: 'var(--fg-base)' }}>
                  创建一个新的 http.Handler，用于处理请求路由...
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  )
}
