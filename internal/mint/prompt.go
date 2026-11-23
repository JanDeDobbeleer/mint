package mint

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"golang.org/x/term"
)

type secretPrompter interface {
	password(string) ([]byte, error)
	confirm(string) (bool, error)
}

type terminalPrompter struct {
	stdin  io.Reader
	stderr io.Writer
}

func (p terminalPrompter) terminal() (*os.File, func(), error) {
	if file, ok := p.stdin.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		return file, func() {}, nil
	}
	path := "/dev/tty"
	if runtime.GOOS == "windows" {
		path = "CONIN$"
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, errors.New("an interactive terminal is required for secret prompts")
	}
	if !term.IsTerminal(int(file.Fd())) {
		_ = file.Close()
		return nil, nil, errors.New("an interactive terminal is required for secret prompts")
	}
	return file, func() { _ = file.Close() }, nil
}

func (p terminalPrompter) password(message string) ([]byte, error) {
	file, closeTerminal, err := p.terminal()
	if err != nil {
		return nil, err
	}
	defer closeTerminal()
	if _, err := fmt.Fprint(p.stderr, message); err != nil {
		return nil, errors.New("cannot write the passphrase prompt")
	}
	password, err := term.ReadPassword(int(file.Fd()))
	_, _ = fmt.Fprintln(p.stderr)
	if err != nil {
		clear(password)
		return nil, errors.New("cannot read the secret")
	}
	return password, nil
}

func (p terminalPrompter) confirm(message string) (bool, error) {
	file, closeTerminal, err := p.terminal()
	if err != nil {
		return false, err
	}
	defer closeTerminal()
	if _, err := fmt.Fprint(p.stderr, message); err != nil {
		return false, errors.New("cannot write the confirmation prompt")
	}
	reply, err := bufio.NewReader(file).ReadString('\n')
	if err != nil {
		return false, errors.New("cannot read the confirmation")
	}
	return strings.EqualFold(strings.TrimSpace(reply), "yes"), nil
}
