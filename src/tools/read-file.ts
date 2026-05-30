import { z } from 'zod';
import { readFile } from 'fs/promises';
import type { Tool } from './types';

const ReadFileParams = z.object({
  path: z.string().min(1),
});

export const readFileTool: Tool = {
  name: 'read_file',
  description: 'Read the contents of a file from the filesystem.',
  parameters: ReadFileParams,
  async execute(args) {
    const { path } = ReadFileParams.parse(args);

    try {
      const content = await readFile(path, 'utf-8');
      return `File: ${path}\n\n${content}`;
    } catch (err: any) {
      return `Error reading file ${path}: ${err.message}`;
    }
  },
};
