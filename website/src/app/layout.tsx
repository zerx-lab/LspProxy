import type { Metadata } from 'next'
import { ThemeProvider } from '../components/ThemeProvider'
import './globals.css'

export const metadata: Metadata = {
  title: 'LspProxy — LSP 中文翻译代理',
  description: '透明代理 LSP 消息，实时将 hover、completion、diagnostics 翻译为中文。支持 VSCode、Neovim、Zed。',
  openGraph: {
    title: 'LspProxy',
    description: 'LSP 中文翻译代理 — 实时将编辑器文档翻译为中文',
    url: 'https://lsproxy.dev',
  },
}

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="zh-CN" dir="ltr" suppressHydrationWarning>
      <body>
        <ThemeProvider>
          {children}
        </ThemeProvider>
      </body>
    </html>
  )
}
