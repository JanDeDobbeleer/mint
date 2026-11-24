package vault

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"sync"
)

const maxPasswordLine = 4096

// StdinPrompter supplies the master password from the first line of a reader (the --password-stdin source).
// The line is read once and every call returns a fresh copy, so creating a new vault, which asks twice, works
// non-interactively. It never echoes or prints the password.
type StdinPrompter struct {
	err   error
	r     *bufio.Reader
	value []byte
	once  sync.Once
}

// NewStdinPrompter reads the password from r.
func NewStdinPrompter(r io.Reader) *StdinPrompter { return &StdinPrompter{r: bufio.NewReader(r)} }

// Password implements Prompter; the message is ignored.
func (p *StdinPrompter) Password(string) ([]byte, error) {
	p.once.Do(func() {
		line, err := p.r.ReadString('\n')
		if err != nil && (!errors.Is(err, io.EOF) || line == "") {
			p.err = errors.New("cannot read the master password from standard input")
			return
		}
		if len(line) > maxPasswordLine {
			p.err = errors.New("the master password on standard input is too long")
			return
		}
		p.value = []byte(strings.TrimRight(line, "\r\n"))
	})
	if p.err != nil {
		return nil, p.err
	}
	return append([]byte(nil), p.value...), nil
}

// Close zeroizes the cached password.
func (p *StdinPrompter) Close() { clear(p.value) }
