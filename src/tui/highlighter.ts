import { createHighlighter } from 'shiki'

let highlighterPromise: ReturnType<typeof createHighlighter> | null = null

export async function getHighlighter() {
  if (!highlighterPromise) {
    highlighterPromise = createHighlighter({
      themes: ['github-dark', 'github-light'],
      langs: [
        'typescript',
        'javascript',
        'tsx',
        'jsx',
        'json',
        'python',
        'rust',
        'go',
        'bash',
        'shell',
        'markdown',
        'css',
        'html',
        'sql',
        'yaml',
        'toml',
        'dockerfile',
      ],
    })
  }
  return highlighterPromise
}

export async function highlightCode(code: string, lang: string = 'text'): Promise<string> {
  try {
    const highlighter = await getHighlighter()
    return highlighter.codeToAnsi(code, {
      lang: lang || 'text',
      theme: 'github-dark',
    })
  } catch (error) {
    // Fallback to plain text if highlighting fails
    return code
  }
}
