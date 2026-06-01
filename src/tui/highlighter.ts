import { createHighlighter } from 'shiki';

export type HighlightedToken = {
  content: string;
  color?: string;
};

export type HighlightedLine = HighlightedToken[];

let highlighterPromise: ReturnType<typeof createHighlighter> | undefined;

export async function highlightCode(code: string, lang = 'text'): Promise<HighlightedLine[]> {
  try {
    highlighterPromise ??= createHighlighter({
      themes: ['github-dark'],
      langs: [
        'bash',
        'css',
        'dockerfile',
        'go',
        'html',
        'javascript',
        'json',
        'jsx',
        'markdown',
        'python',
        'rust',
        'shell',
        'sql',
        'toml',
        'tsx',
        'typescript',
        'yaml',
      ],
    });

    const highlighter = await highlighterPromise;
    const result = highlighter.codeToTokens(code, {
      lang: (lang || 'text') as any,
      theme: 'github-dark',
    });

    return result.tokens.map((line) =>
      line.map((token) => ({
        content: token.content,
        color: token.color,
      })),
    );
  } catch {
    return code.split(/\r?\n/).map((line) => [{ content: line }]);
  }
}
