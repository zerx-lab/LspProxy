import { useMDXComponents as getThemeComponents } from 'nextra-theme-docs'

export function useMDXComponents(components: Record<string, unknown>) {
  return {
    ...getThemeComponents(),
    ...components,
  }
}
