import { generateStaticParamsFor, importPage } from 'nextra/pages'
import { useMDXComponents } from '../../../../mdx-components'

export const generateStaticParams = generateStaticParamsFor('mdxPath')

type PageProps = {
  params: Promise<{ mdxPath?: string[] }>
}

// app/docs/[[...mdxPath]] 收到的 segments 不含 "docs" 前缀，
// 但 nextra content root 是 src/content/，key 格式是 "docs/xxx"。
// 因此需要手动补全前缀。
function resolveSegments(mdxPath: string[] | undefined): string[] {
  if (!mdxPath || mdxPath.length === 0) {
    return ['docs']          // /docs → docs/index.mdx
  }
  return ['docs', ...mdxPath] // /docs/foo → docs/foo.mdx
}

export async function generateMetadata(props: PageProps) {
  const params = await props.params
  const { metadata } = await importPage(resolveSegments(params.mdxPath))
  return metadata
}

export default async function Page(props: PageProps) {
  const params = await props.params
  const segments = resolveSegments(params.mdxPath)
  const { default: MDXContent, toc, metadata } = await importPage(segments)
  const components = useMDXComponents({})
  return <MDXContent toc={toc} metadata={metadata} components={components} params={params} />
}
