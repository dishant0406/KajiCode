import { toolRegistry } from './registry';
import { readFileTool } from './read-file';
import { bashTool } from './bash';
import { editFileTool } from './edit-file';
import { planTool } from './plan';
import { listDirectoryTool } from './list-directory';
import { grepTool } from './grep';

toolRegistry.register(readFileTool);
toolRegistry.register(bashTool);
toolRegistry.register(editFileTool);
toolRegistry.register(planTool);
toolRegistry.register(listDirectoryTool);
toolRegistry.register(grepTool);

export { toolRegistry };
export * from './types';
export { getCurrentPlan, clearPlan } from './plan';
export type { PlanItem } from './plan';
