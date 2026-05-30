import React from 'react';
import { render } from 'ink';
import { App } from './App';
import { getHighlighter } from './highlighter';

// Preload Shiki highlighter early for fast syntax highlighting
getHighlighter().catch(() => {
  // Silently fail - we'll fallback gracefully
});

export function startTUI() {
  render(<App />);
}
