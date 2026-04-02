'use client'

const KW_RE = /\b(proxy|lspproxy|Config|Google|OpenAI|OpenAIConfig|New|os|err|if|nil|gopls)\b/g

type Tok = { t: string; c: string }
function tokenizeLine(line: string): Tok[] {
  if (/^\s*\/\//.test(line)) return [{ t: line, c: 'var(--code-comment)' }]
  const toks: Tok[] = []
  let rest = line
  while (rest.length > 0) {
    const ci = rest.indexOf('//')
    const sm = rest.match(/"[^"]*"/)
    const cp = ci === -1 ? Infinity : ci
    const sp = sm?.index ?? Infinity
    if (cp < sp) {
      if (cp > 0) toks.push(...kwTokens(rest.slice(0, cp)))
      toks.push({ t: rest.slice(cp), c: 'var(--code-comment)' })
      break
    } else if (sp < Infinity && sm) {
      if (sp > 0) toks.push(...kwTokens(rest.slice(0, sp)))
      toks.push({ t: sm[0], c: 'var(--code-string)' })
      rest = rest.slice(sp + sm[0].length)
    } else {
      toks.push(...kwTokens(rest))
      break
    }
  }
  return toks
}
function kwTokens(text: string): Tok[] {
  const out: Tok[] = []
  let last = 0
  const re = new RegExp(KW_RE.source, 'g')
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) out.push({ t: text.slice(last, m.index), c: 'var(--code-default)' })
    out.push({ t: m[0], c: 'var(--code-keyword)' })
    last = m.index + m[0].length
  }
  if (last < text.length) out.push({ t: text.slice(last), c: 'var(--code-default)' })
  return out
}

const translators = [
  {
    name: 'Google',
    badge: '免费',
    badgeColor: 'emerald',
    code: `proxy := lspproxy.New(lspproxy.Config{
  Command:    "rust-analyzer",
  Translator: lspproxy.Google(),
})`,
    features: ['无需 API Key', '免费不限量', '自动重试'],
  },
  {
    name: 'OpenAI 兼容',
    badge: '可配置',
    badgeColor: 'violet',
    code: `proxy := lspproxy.New(lspproxy.Config{
  Command: "gopls",
  Translator: lspproxy.OpenAI(lspproxy.OpenAIConfig{
    Model:   "deepseek-chat",
    BaseURL: "https://api.deepseek.com/v1",
    APIKey:  os.Getenv("DEEPSEEK_API_KEY"),
  }),
})`,
    features: ['DeepSeek / Qwen', 'Ollama 本地模型', '自定义 Prompt'],
  },
]

export function TranslatorSection() {
  return (
    <section
      className="relative py-20 px-6 sm:px-10 transition-colors duration-300"
      style={{ background: 'var(--bg-surface)', borderTop: '1px solid var(--border)' }}
    >
      <div className="max-w-6xl mx-auto">
        <div className="mb-12">
          <p className="font-mono text-xs uppercase tracking-widest mb-3 transition-colors duration-300"
            style={{ color: 'var(--fg-faint)' }}>翻译引擎</p>
          <h2 className="text-2xl sm:text-3xl font-medium tracking-tight max-w-lg transition-colors duration-300"
            style={{ color: 'var(--fg-strong)' }}>
            根据需求选择翻译引擎
          </h2>
          <p className="mt-3 text-sm max-w-md transition-colors duration-300" style={{ color: 'var(--fg-muted)' }}>
            内置两种翻译器，可随时切换。Google 翻译开箱即用，OpenAI 兼容接口支持更精准的 AI 翻译。
          </p>
        </div>

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
          {translators.map((t) => (
            <div key={t.name} className="overflow-hidden rounded-lg transition-colors duration-300"
              style={{ border: '1px solid var(--border)', background: 'var(--bg-base)' }}>

              {/* Card header */}
              <div className="px-5 py-4 space-y-2.5 transition-colors duration-300"
                style={{ borderBottom: '1px solid var(--border)' }}>
                <div className="flex items-center gap-2.5">
                  <span className="text-sm font-medium whitespace-nowrap transition-colors duration-300"
                    style={{ color: 'var(--fg-base)' }}>{t.name}</span>
                  <span
                    className="shrink-0 px-2 py-0.5 rounded-full text-xs font-mono whitespace-nowrap"
                    style={t.badgeColor === 'emerald'
                      ? { background: 'rgba(16,185,129,0.1)', color: 'rgb(16,185,129)' }
                      : { background: 'rgba(139,92,246,0.1)', color: 'rgb(139,92,246)' }}
                  >
                    {t.badge}
                  </span>
                </div>
                <div className="flex flex-wrap gap-1.5">
                  {t.features.map((feat) => (
                    <span key={feat}
                      className="text-xs px-2 py-0.5 rounded whitespace-nowrap transition-colors duration-300"
                      style={{ border: '1px solid var(--border)', color: 'var(--fg-faint)', background: 'var(--bg-surface)' }}>
                      {feat}
                    </span>
                  ))}
                </div>
              </div>

              {/* Code */}
              <div className="p-5 transition-colors duration-300" style={{ background: 'var(--bg-code)' }}>
                <pre className="text-xs leading-relaxed overflow-x-auto code-scrollbar" style={{ fontFamily: 'monospace' }}>
                  {t.code.split('\n').map((line, i) => (
                    <div key={i} style={{ display: 'flex', gap: '0.75rem' }}>
                      <span style={{ color: 'var(--code-linenum)', minWidth: '1rem', textAlign: 'right', userSelect: 'none', flexShrink: 0 }}>
                        {i + 1}
                      </span>
                      <span>
                        {tokenizeLine(line).map((tok, j) => (
                          <span key={j} style={{ color: tok.c }}>{tok.t}</span>
                        ))}
                      </span>
                    </div>
                  ))}
                </pre>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  )
}
