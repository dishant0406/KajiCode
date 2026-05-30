import React, { useState } from 'react';
import { Box, Text, useApp, useInput } from 'ink';
import { ProviderPicker } from './ProviderPicker';
import { AddProvider } from './AddProvider';
import { Logo } from './Logo';
import { ThinkingSpinner } from './Spinner';
import { MessageRenderer } from './MessageRenderer';
import { ToolCallRenderer } from './ToolCallRenderer';
import { configManager } from '../config/manager';
import { loadProviderConfig } from '../config/provider';
import { OpenAIProvider } from '../providers/openai';
import { runAgent } from '../agent/loop';

type Screen = 'chat' | 'provider-picker' | 'add-provider';

type ChatMessage =
  | { type: 'user'; content: string }
  | { type: 'assistant'; content: string }
  | { type: 'tool-call'; name: string; args: string; result?: string }
  | { type: 'tool-result'; content: string } // legacy - results now attach to tool-call
  | { type: 'system'; content: string };

export const App: React.FC = () => {
  const { exit } = useApp();
  const [screen, setScreen] = useState<Screen>('chat');
  const [input, setInput] = useState('');
  const [messages, setMessages] = useState<ChatMessage[]>([
    { type: 'system', content: 'Welcome to zero. Type /provider to manage providers.' },
    { type: 'system', content: 'Type /help for available commands.' },
  ]);

  // Check on startup if we have any usable provider
  React.useEffect(() => {
    const checkProvider = async () => {
      try {
        await loadProviderConfig();
      } catch (err: any) {
        if (err.message?.includes('No LLM provider configured')) {
          setMessages((prev) => [
            ...prev,
            { 
              type: 'system', 
              content: '⚠️  No provider configured yet. Use /provider to add one (OpenGateway recommended).' 
            }
          ]);
        }
      }
    };
    checkProvider();
  }, []);
  const [isThinking, setIsThinking] = useState(false);
  const [streamingMessageIndex, setStreamingMessageIndex] = useState<number | null>(null);

  // Plan Mode (inspired by OpenClaude / Claude Code)
  const [isPlanMode, setIsPlanMode] = useState(false);

  // Scrolling state (Grok Build style internal scrolling)
  const [scrollOffset, setScrollOffset] = useState(0);
  const [terminalRows, setTerminalRows] = useState(24); // default fallback

  // Current provider info for the input bar (Grok Build style)
  const activeProfile = configManager.getActiveProvider();
  const currentProviderName = activeProfile?.name || (process.env.ZERO_PROVIDER_COMMAND ? 'command' : 'env');
  const currentModel = activeProfile?.model || process.env.OPENAI_MODEL || 'default';

  // Track terminal size for proper scrolling
  React.useEffect(() => {
    const updateSize = () => {
      setTerminalRows(process.stdout.rows || 24);
    };
    process.stdout.on('resize', updateSize);
    updateSize();
    return () => {
      process.stdout.off('resize', updateSize);
    };
  }, []);

  // Auto-scroll to bottom when new messages arrive (unless user scrolled up)
  React.useEffect(() => {
    // Only auto-scroll if user is near the bottom
    if (scrollOffset <= 3) {
      setScrollOffset(0);
    }
  }, [messages.length]);

  // Only capture main chat input when we're actually in the chat screen
  const isInChat = screen === 'chat';

  useInput((inputChar, key) => {
    if (key.ctrl && inputChar === 'c') {
      exit();
      return;
    }

    // Don't process chat input while in provider picker or add flow
    if (!isInChat) return;

    // Scrolling controls (when input is empty)
    if (!input) {
      if (key.upArrow) {
        setScrollOffset((prev) => Math.min(prev + 1, messages.length - 1));
        return;
      }
      if (key.downArrow) {
        setScrollOffset((prev) => Math.max(prev - 1, 0));
        return;
      }
      if (key.pageUp) {
        setScrollOffset((prev) => Math.min(prev + 8, messages.length - 1));
        return;
      }
      if (key.pageDown) {
        setScrollOffset((prev) => Math.max(prev - 8, 0));
        return;
      }
      if (key.home) {
        setScrollOffset(messages.length - 1);
        return;
      }
      if (key.end) {
        setScrollOffset(0);
        return;
      }
    }

    if (key.return) {
      handleSubmit();
      return;
    }

    if (key.backspace || key.delete) {
      setInput((prev) => prev.slice(0, -1));
      return;
    }

    if (inputChar && !key.ctrl && !key.meta) {
      setInput((prev) => prev + inputChar);
    }
  }, { isActive: isInChat });

  const handleSubmit = () => {
    if (!input.trim()) return;

    const trimmed = input.trim();
    setInput('');

    // Handle slash commands
    if (trimmed.startsWith('/')) {
      setMessages((prev) => [...prev, { type: 'user', content: trimmed }]);
      handleSlashCommand(trimmed);
      return;
    }

    // Regular message → send to agent
    setMessages((prev) => [...prev, { type: 'user', content: trimmed }]);

    const runAgentLoop = async () => {
      setIsThinking(true);

      try {
        const providerConfig = await loadProviderConfig();
        const provider = new OpenAIProvider({
          apiKey: providerConfig.apiKey || '',
          baseURL: providerConfig.baseURL,
          model: providerConfig.model,
        });

        // Add empty assistant message that we'll stream into
        setMessages((prev) => {
          const newMessages = [...prev, { type: 'assistant' as const, content: '' }];
          setStreamingMessageIndex(newMessages.length - 1);
          return newMessages;
        });

        await runAgent(trimmed, provider, {
          onText: (text: string) => {
            setIsThinking(false);
            setMessages((prev) => {
              const newMessages = [...prev];
              const idx = streamingMessageIndex ?? newMessages.length - 1;

              if (newMessages[idx]?.type === 'assistant') {
                const current = newMessages[idx] as { type: 'assistant'; content: string };
                newMessages[idx] = {
                  ...current,
                  content: current.content + text,
                };
              }
              return newMessages;
            });
          },
          onToolCall: (tc) => {
            setIsThinking(false);
            setMessages((prev) => [
              ...prev,
              { type: 'tool-call', name: tc.name, args: tc.arguments },
            ]);
            // Reset streaming index since we inserted a message
            setStreamingMessageIndex(null);
          },
          onToolResult: (result) => {
            // Attach result to the most recent tool call that doesn't have one yet
            setMessages((prev) => {
              const newMessages = [...prev];
              for (let i = newMessages.length - 1; i >= 0; i--) {
                const msg = newMessages[i];
                if (msg && msg.type === 'tool-call' && (msg as any).result === undefined) {
                  (newMessages as any)[i] = {
                    ...msg,
                    result: result.result,
                  };
                  break;
                }
              }
              return newMessages;
            });
          },
        });
      } catch (err: any) {
        setIsThinking(false);

        let friendlyMessage = err.message || String(err);

        // Make common provider/auth errors more helpful
        if (friendlyMessage.includes('401') || friendlyMessage.toLowerCase().includes('unauthorized') || friendlyMessage.toLowerCase().includes('invalid api key')) {
          friendlyMessage = 'Provider error: Invalid or missing API key. Use /provider to fix your credentials.';
        } else if (friendlyMessage.includes('No LLM provider configured')) {
          friendlyMessage = 'No provider set up. Type /provider to add one.';
        } else if (friendlyMessage.toLowerCase().includes('connection') || friendlyMessage.toLowerCase().includes('network')) {
          friendlyMessage = `Provider connection error: ${friendlyMessage}`;
        }

        setMessages((prev) => [...prev, { type: 'system', content: `Error: ${friendlyMessage}` }]);
      } finally {
        setIsThinking(false);
        setStreamingMessageIndex(null);
      }
    };

    runAgentLoop();
  };

  const handleSlashCommand = (command: string) => {
    const cmd = command.toLowerCase();

    if (cmd === '/provider') {
      setScreen('provider-picker');
      return;
    }

    if (cmd === '/plan') {
      setIsPlanMode(prev => {
        const next = !prev;
        setMessages((msgs) => [
          ...msgs,
          { 
            type: 'system', 
            content: next 
              ? 'Plan mode enabled. The agent will focus on planning before making changes.' 
              : 'Plan mode disabled.' 
          },
        ]);
        return next;
      });
      return;
    }

    if (cmd === '/help') {
      setMessages((prev) => [
        ...prev,
        { type: 'system', content: 'Available commands:' },
        { type: 'system', content: '  /provider  - Manage LLM providers (fix provider errors here)' },
        { type: 'system', content: '  /plan      - Toggle Plan Mode (green border)' },
        { type: 'system', content: '  /help      - Show this help' },
        { type: 'system', content: '  /exit      - Quit' },
      ]);
      return;
    }

    if (cmd === '/exit' || cmd === '/quit') {
      exit();
      return;
    }

    setMessages((prev) => [...prev, { type: 'system', content: `Unknown command: ${command}` }]);
  };

  const handleProviderSelected = (name: string) => {
    const success = configManager.setActiveProvider(name);
    if (success) {
      setMessages((prev) => [...prev, { type: 'system', content: `Switched to provider: ${name}` }]);
    }
    setScreen('chat');
  };

  const handleProviderPickerCancel = () => {
    setScreen('chat');
  };

  const handleOpenAddProvider = () => {
    setScreen('add-provider');
  };

  const handleAddProviderDone = (providerName?: string) => {
    setScreen('chat');

    if (providerName) {
      // Automatically switch to the newly added provider
      const switched = configManager.setActiveProvider(providerName);

      if (switched) {
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Added and switched to provider: ${providerName}` },
        ]);
      } else {
        setMessages((prev) => [
          ...prev,
          { type: 'system', content: `Provider added: ${providerName}` },
        ]);
      }
    } else {
      setMessages((prev) => [...prev, { type: 'system', content: 'Provider added successfully.' }]);
    }
  };

  const handleAddProviderCancel = () => {
    setScreen('provider-picker');
  };

  if (screen === 'add-provider') {
    return (
      <AddProvider
        onDone={handleAddProviderDone}
        onCancel={handleAddProviderCancel}
      />
    );
  }

  if (screen === 'provider-picker') {
    return (
      <ProviderPicker
        onSelect={handleProviderSelected}
        onCancel={handleProviderPickerCancel}
        onAddNew={handleOpenAddProvider}
      />
    );
  }

  const showLogo = messages.length <= 2;

  // Calculate visible messages for scrolling (Grok Build style)
  const chatHeight = Math.max(8, terminalRows - 6); // leave room for input + status
  const visibleMessages = messages.slice(scrollOffset, scrollOffset + chatHeight);

  const canScrollUp = scrollOffset < messages.length - 1;
  const canScrollDown = scrollOffset > 0;

  return (
    <Box flexDirection="column" height="100%">
      {/* Scrollable messages area with right-side scroll indicator (Grok Build style) */}
      <Box 
        flexGrow={1} 
        flexDirection="row"
        overflow="hidden"
      >
        {/* Main chat content */}
        <Box 
          flexGrow={1} 
          flexDirection="column" 
          paddingX={1} 
          paddingTop={1}
        >
        {showLogo && <Logo />}

        {/* Scroll indicator */}
        {(canScrollUp || canScrollDown) && (
          <Text color="gray" dimColor>
            {canScrollUp ? '↑ ' : '  '}Scroll with ↑↓ / PgUp/PgDn / Home/End {canScrollDown ? '↓' : ''}
          </Text>
        )}

        <Box flexDirection="column">
          {visibleMessages.map((msg, index) => {
            const realIndex = scrollOffset + index;

            if (msg.type === 'user') {
              return (
                <Box key={realIndex} marginBottom={1}>
                  <Text color="blueBright">
                    {`> ${msg.content}`}
                  </Text>
                </Box>
              );
            }

            if (msg.type === 'assistant') {
              const isStreaming = realIndex === streamingMessageIndex;
              return (
                <Box key={realIndex} marginBottom={1} flexDirection="row">
                  <Text color="cyan" dimColor>● </Text>
                  <Box flexDirection="column" flexGrow={1}>
                    <MessageRenderer content={msg.content} />
                    {isStreaming && (
                      <Text color="cyan" dimColor>▌</Text>
                    )}
                  </Box>
                </Box>
              );
            }

            if (msg.type === 'tool-call') {
              const hasResult = !!msg.result;
              return (
                <Box key={realIndex} marginBottom={0}>
                  <ToolCallRenderer
                    name={msg.name}
                    args={msg.args}
                    result={msg.result}
                    status={hasResult ? 'success' : 'running'}
                  />
                </Box>
              );
            }

            if (msg.type === 'tool-result') {
              // Legacy separate results are no longer created; ignore for cleanliness
              return null;
            }

            // system messages
            return (
              <Box key={realIndex} marginBottom={1}>
                <Text color="gray" dimColor>
                  {msg.content}
                </Text>
              </Box>
            );
          })}

          {isThinking && <ThinkingSpinner />}
        </Box>
        </Box>
      </Box>

      {/* Scroll position (Grok Build style) */}
      {(canScrollUp || canScrollDown) && (
        <Box paddingX={1} justifyContent="flex-end">
          <Text color="gray" dimColor>
            {scrollOffset + 1}/{messages.length}{canScrollUp ? ' ↑' : ''}{canScrollDown ? ' ↓' : ''}
          </Text>
        </Box>
      )}

      {/* Input box at the bottom */}
      <Box
        borderStyle="single"
        borderColor={isPlanMode ? 'green' : 'gray'}
        paddingX={1}
        paddingY={0}
        flexDirection="row"
        justifyContent="space-between"
        alignItems="center"
      >
        {/* Left: prompt + input */}
        <Box flexDirection="row">
          <Text color={isPlanMode ? 'green' : 'greenBright'}>› </Text>
          <Text color="white">{input}</Text>
          <Text color="gray">█</Text>
        </Box>

        {/* Right: Current provider + model */}
        <Box flexDirection="row">
          <Text color="cyan" bold>{currentProviderName}</Text>
          <Text color="gray"> • </Text>
          <Text color="magenta" dimColor>{currentModel}</Text>
        </Box>
      </Box>

      {/* Very subtle status line */}
      <Box paddingX={1} flexDirection="row">
        <Text color="gray" dimColor>
          /help • ↑↓ scroll • Ctrl+C exit
        </Text>
        {isPlanMode && (
          <Text color="green"> • PLAN MODE</Text>
        )}
      </Box>
    </Box>
  );
};

