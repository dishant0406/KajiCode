import { z } from 'zod';

export interface Tool {
  name: string;
  description: string;
  parameters: z.ZodObject<any>; // Zod schema for validation
  execute: (args: any) => Promise<string>; // Returns tool result as string
}

export interface ToolCall {
  id: string;
  name: string;
  arguments: string; // raw JSON string from model
}

export interface ToolResult {
  toolCallId: string;
  result: string;
}
