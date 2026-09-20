package agents

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	errAgentNameRequired = errors.New("agent name is required")
	errPromptRequired    = errors.New("agent prompt is required")
)

func errUnknownAgent(name string, available []string) error {
	list := "(none registered)"
	if len(available) > 0 {
		list = strings.Join(available, ", ")
	}
	return fmt.Errorf("agent %q not found. Available: %s. Pick one whose tools fit the task", name, list)
}

func errDepthExceeded(depth, cap int) error {
	return fmt.Errorf("spawning an agent at depth %d would exceed maximum nesting depth %d", depth+1, cap)
}

func errArgType(key, want string) error {
	return fmt.Errorf("%s must be a %s", key, want)
}

// randomToken returns a short random hex token for task ids.
func randomToken() string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "0000000000000000"
	}
	return hex.EncodeToString(random)
}
