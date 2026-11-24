package mint

import (
	"os"
	"testing"

	"github.com/JanDeDobbeleer/mint/internal/agent/copilot/copilottest"
)

func TestMain(m *testing.M) {
	if copilottest.IsFake() {
		os.Exit(copilottest.Run())
	}
	os.Exit(m.Run())
}
