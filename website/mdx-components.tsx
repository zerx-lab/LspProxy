import { useMDXComponents as getThemeComponents } from 'nextra-theme-docs'
import type { MDXComponents } from 'nextra/mdx-components'

export function useMDXComponents(components: MDXComponents): MDXComponents {
  return {
    ...getThemeComponents(),
    ...components,
  }
}
