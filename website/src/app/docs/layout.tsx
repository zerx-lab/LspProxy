import { Footer, Layout, Navbar } from 'nextra-theme-docs'
import { Head } from 'nextra/components'
import { getPageMap } from 'nextra/page-map'

const navbar = (
  <Navbar
    logo={
      <div className="flex items-center gap-2">
        <svg width="24" height="18" viewBox="0 0 60 45" fill="none" xmlns="http://www.w3.org/2000/svg">
          <path
            fillRule="evenodd"
            clipRule="evenodd"
            d="M0 0H15V15H30V30H15V45H0V30V15V0ZM45 30V15H30V0H45H60V15V30V45H45H30V30H45Z"
            className="fill-current"
          />
        </svg>
        <span style={{ fontFamily: 'monospace', fontWeight: 700, fontSize: '0.95rem', textTransform: 'uppercase', letterSpacing: '0.05em' }}>
          LSP-PROXY.
        </span>
      </div>
    }
    projectLink="https://github.com/zerx-lab/LspProxy"
  />
)

const footer = (
  <Footer>
    MIT {new Date().getFullYear()} © zerx-lab
  </Footer>
)

export default async function DocsLayout({
  children,
}: {
  children: React.ReactNode
}) {
  const pageMap = await getPageMap('/docs')
  return (
    <html lang="zh-CN" dir="ltr" suppressHydrationWarning>
      <Head />
      <body>
        <Layout
          navbar={navbar}
          pageMap={pageMap}
          docsRepositoryBase="https://github.com/zerx-lab/LspProxy"
          footer={footer}
          sidebar={{ defaultMenuCollapseLevel: 1 }}
        >
          {children}
        </Layout>
      </body>
    </html>
  )
}
