package tui

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type classifierCommandOrigin int

const (
	classifierCommandOriginTranscript classifierCommandOrigin = iota
	classifierCommandOriginWizard
)

type classifierCommandRequest struct {
	id     int
	origin classifierCommandOrigin
	args   []string
	// stdin carries a secret (an API key) to the CLI on stdin so it never appears
	// in argv or the transcript.
	stdin string
	raw   string
}

type classifierCommandResultMsg struct {
	request classifierCommandRequest
	result  ClassifierCommandResult
}

// startClassifierTranscriptCommand runs `/classifier <args>` as a CLI call. A bare
// `/classifier` defaults to `list`.
func (m model) startClassifierTranscriptCommand(args string) (model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		args = "list"
	}
	parsedArgs, err := splitMCPCommandArgs(args)
	if err != nil {
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "classifier", text: "Classifier action failed\n" + err.Error()})
		return m, nil
	}
	return m.startClassifierCommand(classifierCommandRequest{origin: classifierCommandOriginTranscript, raw: args, args: parsedArgs})
}

func (m model) startClassifierCommand(request classifierCommandRequest) (model, tea.Cmd) {
	if m.classifierCommand == nil {
		result := ClassifierCommandResult{ExitCode: 1, Error: "classifier action unavailable"}
		return m.applyClassifierCommandResultMessage(classifierCommandResultMsg{request: request, result: result}), nil
	}
	m.cancelClassifierCommand()
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	m.classifierCommandSeq++
	request.id = m.classifierCommandSeq
	request.args = append([]string{}, request.args...)
	m.classifierCommandCancel = cancel
	runner := m.classifierCommand
	stdin := request.stdin
	return m, func() tea.Msg {
		return classifierCommandResultMsg{request: request, result: runner(ctx, request.args, stdin)}
	}
}

func (m *model) cancelClassifierCommand() {
	if m.classifierCommandCancel != nil {
		m.classifierCommandCancel()
		m.classifierCommandCancel = nil
		m.classifierCommandSeq++
	}
}

func (m model) applyClassifierCommandResultMessage(msg classifierCommandResultMsg) model {
	if msg.request.id != 0 && msg.request.id != m.classifierCommandSeq {
		return m
	}
	m.classifierCommandCancel = nil
	switch msg.request.origin {
	case classifierCommandOriginWizard:
		m = m.applyClassifierFormSaveResult(msg.result)
	default:
		text := m.classifierResultText(msg.request.raw, msg.result)
		m.transcript = appendTranscriptRow(m.transcript, transcriptRow{kind: rowSystem, tool: "classifier", text: text})
	}
	return m
}

func (m model) classifierResultText(raw string, result ClassifierCommandResult) string {
	output := strings.TrimSpace(result.Output)
	errText := strings.TrimSpace(result.Error)
	if result.ExitCode != 0 || errText != "" {
		message := errText
		if message == "" {
			message = output
		}
		if message == "" {
			message = "classifier command failed"
		}
		return strings.Join([]string{"Classifier action failed", message}, "\n")
	}
	if output == "" {
		output = "kajicode classifier " + raw
	}
	return output
}
