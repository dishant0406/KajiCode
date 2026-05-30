export interface Message {
  role: 'system' | 'user' | 'assistant' | 'tool';
  content: string;
  toolCalls?: ToolCall[];
  toolCallId?: string;
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: string; // JSON string
}

export interface ToolDefinition {
  name: string;
  description: string;
  parameters: Record<string, unknown>; // JSON Schema
}

export type StreamEvent =
  | { type: 'text'; content: string }
  | { type: 'tool-call-start'; id: string; name: string }
  | { type: 'tool-call-delta'; id: string; argumentsFragment: string }
  | { type: 'tool-call-end'; id: string }
  | { type: 'usage'; promptTokens: number; completionTokens: number }
  | { type: 'done' };

export interface Provider {
  streamCompletion(
    messages: Message[],
    tools: ToolDefinition[]
  ): AsyncIterable<StreamEvent>;
}