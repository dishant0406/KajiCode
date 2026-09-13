package mcp

import (
	"encoding/json"
)

// ToolListChangedMethod is the MCP notification a server sends when its tool
// list changes (a tool was added, removed, or updated).
const ToolListChangedMethod = "notifications/tools/list_changed"

// InstructionsProvider is implemented by a ToolClient that captured the
// server's `initialize` instructions. Optional: a client that does not
// implement it simply has no instructions to surface.
type InstructionsProvider interface {
	Instructions() string
}

// NotificationSubscriber is implemented by a ToolClient that can deliver
// server-initiated notifications. Optional.
type NotificationSubscriber interface {
	OnNotification(handler func(method string, params json.RawMessage))
}

// subscribeToolListChanged registers handler for the tool-list-changed
// notification on a client that supports notifications. It is a no-op for
// clients that do not implement NotificationSubscriber (e.g. test fakes).
func subscribeToolListChanged(client ToolClient, handler func()) {
	subscriber, ok := client.(NotificationSubscriber)
	if !ok {
		return
	}
	subscriber.OnNotification(func(method string, params json.RawMessage) {
		if method == ToolListChangedMethod {
			handler()
		}
	})
}

// serverInstructions returns the server's initialize instructions, or "" when the
// client does not expose them.
func serverInstructions(client ToolClient) string {
	provider, ok := client.(InstructionsProvider)
	if !ok {
		return ""
	}
	return provider.Instructions()
}

// CapabilityProvider is implemented by a ToolClient that captured the server's
// `initialize` capabilities. Optional: a client that does not implement it is
// treated as supporting nothing (its optional surfaces stay unavailable).
type CapabilityProvider interface {
	SupportsResources() bool
	SupportsPrompts() bool
}

// serverSupportsResources reports whether a server advertised the resources
// capability during initialize. A client that cannot answer is treated as
// unsupported, so the resource tools skip it rather than erroring.
func serverSupportsResources(client ToolClient) bool {
	provider, ok := client.(CapabilityProvider)
	if !ok {
		return false
	}
	return provider.SupportsResources()
}

// serverSupportsPrompts reports whether a server advertised the prompts
// capability during initialize. A client that cannot answer is treated as
// unsupported.
func serverSupportsPrompts(client ToolClient) bool {
	provider, ok := client.(CapabilityProvider)
	if !ok {
		return false
	}
	return provider.SupportsPrompts()
}
