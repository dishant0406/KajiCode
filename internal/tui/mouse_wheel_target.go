package tui

type transcriptWheelTargetKey struct {
	width              int
	height             int
	transcriptLen      int
	inputCursor        int
	queuedBytes        int
	planLen            int
	pastePreviewCount  int
	pendingImageCount  int
	pendingDocCount    int
	mouseCapture       bool
	mouseReleased      bool
	transcriptDetailed bool
	setupVisible       bool
	providerWizard     bool
	mcpAddWizard       bool
	mcpManager         bool
	picker             bool
	suggestions        bool
	pendingAskUser     bool
	home               bool
	subchat            bool
	sidebarHidden      bool
	hidePinnedPlan     bool
	planExpanded       bool
	composerActive     bool
}

func transcriptWheelTargetKeyForModel(m model) transcriptWheelTargetKey {
	return transcriptWheelTargetKey{
		width:              m.width,
		height:             m.height,
		transcriptLen:      len(m.transcript),
		inputCursor:        m.input.Position(),
		queuedBytes:        len(m.queuedMessage),
		planLen:            len(m.plan.steps),
		pastePreviewCount:  len(m.composerPastePreviews),
		pendingImageCount:  len(m.pendingImageLabels),
		pendingDocCount:    len(m.pendingDocuments),
		mouseCapture:       m.mouseCapture,
		mouseReleased:      m.mouseReleased,
		transcriptDetailed: m.transcriptDetailed,
		setupVisible:       m.setup.visible,
		providerWizard:     m.providerWizard != nil,
		mcpAddWizard:       m.mcpAddWizard != nil,
		mcpManager:         m.mcpManager != nil,
		picker:             m.picker != nil,
		suggestions:        m.suggestionsActive(),
		pendingAskUser:     m.pendingAskUser != nil,
		home:               m.homePresentationActive(),
		subchat:            m.subchat.active,
		sidebarHidden:      m.sidebarHidden,
		hidePinnedPlan:     m.hidePinnedPlan,
		planExpanded:       m.plan.expanded,
		composerActive:     m.composerActive,
	}
}
