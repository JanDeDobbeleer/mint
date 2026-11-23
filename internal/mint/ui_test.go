package mint

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalUIRedirectedOutput(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	var output bytes.Buffer
	ui := newTerminalUI(&output)
	require.NoError(t, ui.heading("owner/repo"))
	require.NoError(t, ui.step("1", "Create a token", "Select only this repository."))
	require.NoError(t, ui.prompt("Passphrase (hidden): "))
	output.WriteByte('\n')
	require.NoError(t, ui.status("+", "Ready."))
	assert.Equal(t, "\n  mint  owner/repo\n\n"+
		"  1  Create a token\n     Select only this repository.\n\n"+
		"  > Passphrase (hidden): \n  +  Ready.\n", output.String())
	assert.NotContains(t, output.String(), "\x1b")
}

func TestTerminalUIStyledOutput(t *testing.T) {
	var output bytes.Buffer
	ui := terminalUI{out: &output, color: true}
	require.NoError(t, ui.heading("owner/repo"))
	require.NoError(t, ui.prompt("Token (hidden): "))
	assert.Equal(t, "\n  \x1b[1;32mmint\x1b[0m  \x1b[1mowner/repo\x1b[0m\n\n"+
		"  \x1b[32m>\x1b[0m Token (hidden): ", output.String())
}

type failedUIWriter struct{}

func (failedUIWriter) Write([]byte) (int, error) {
	return 0, errors.New("output closed")
}

func TestTerminalUIReportsWriteFailures(t *testing.T) {
	ui := newTerminalUI(failedUIWriter{})
	require.Error(t, ui.heading("Setup"))
	require.Error(t, ui.step("1", "Create a token", "Open GitHub."))
	require.Error(t, ui.status("+", "Ready."))
	require.Error(t, ui.prompt("Token: "))
}
