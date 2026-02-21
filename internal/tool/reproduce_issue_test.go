package tool

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

type MockTool struct {
	name string
}

func (m MockTool) Name() string                                                      { return m.name }
func (m MockTool) Description() string                                               { return "mock" }
func (m MockTool) Parameters() json.RawMessage                                       { return nil }
func (m MockTool) Execute(ctx context.Context, args json.RawMessage) (string, error) { return "", nil }
func (m MockTool) RequiresConfirmation(args json.RawMessage) bool                    { return false }
func (m MockTool) CallString(args json.RawMessage) string                            { return "Mock tool executed" }

func TestRegistry_All_Determinism(t *testing.T) {
	r := NewRegistry()
	// Register enough tools to likely trigger map randomization
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"}
	for _, n := range names {
		r.Register(MockTool{name: n})
	}

	firstRun := r.All()

	// Try multiple times to catch the randomness
	for i := 0; i < 100; i++ {
		currentRun := r.All()
		if !reflect.DeepEqual(firstRun, currentRun) {
			t.Logf("Iteration %d: Order changed!", i)
			t.Fail()
			return
		}
	}
}
