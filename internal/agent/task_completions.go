package agent

// drainTaskCompletions collects finished background sub-agent result blocks
// from the optional TaskCompletionSource. nil (or a panicking source) yields
// no blocks, so a run without the source stays byte-identical to before.
func drainTaskCompletions(source TaskCompletionSource) []string {
	if source == nil {
		return nil
	}
	blocks := source.DrainCompletedTasks()
	if len(blocks) == 0 {
		return nil
	}
	return blocks
}
