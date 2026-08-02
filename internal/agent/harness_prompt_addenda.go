package agent

const openAIPromptAddendum = `<model_guidance>
- Output final responses in GitHub-flavored Markdown: headings for longer answers,
  fenced code blocks for code, and ` + "`inline code`" + ` for paths, commands, and symbols.
- Strongly prefer native file tools (read_file, list_directory, grep, glob,
  write_file, edit_file, apply_patch) over shelling out for file work.
- Persist until the task is fully handled this turn: gather context, implement,
  run validators, and report; do not stop at a partial result.
</model_guidance>`

const geminiPromptAddendum = `<model_guidance>
- Prefer dedicated tools (read_file, grep, glob, edit_file, apply_patch) over
  equivalent shell commands; they are safer and produce cleaner diffs.
- Be concise and concrete. When a shell command has side effects, state why it is needed.
- Use update_plan for multi-step tasks and keep it current.
</model_guidance>`

const anthropicPromptAddendum = `<model_guidance>
- Keep tool-use narration brief and ground conclusions in the latest tool results.
- Avoid re-reading large context that has already been summarized unless the exact
  bytes are needed for an edit or citation.
</model_guidance>`

const openWeightPromptAddendum = `<model_guidance>
- Use exact advertised tool names and JSON fields. If a needed tool is hidden or
  uncertain, call tool_search instead of inventing a tool name.
- Prefer small, verifiable tool calls over broad shell commands. After editing,
  run the narrowest meaningful validation before finalizing.
- Do not guess file contents, command results, or git state; inspect them.
</model_guidance>`
