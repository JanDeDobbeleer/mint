package mint

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

type terminalUI struct {
	out   io.Writer
	color bool
}

func newTerminalUI(out io.Writer) terminalUI {
	file, ok := out.(*os.File)
	_, noColor := os.LookupEnv("NO_COLOR")
	color := ok && !noColor && os.Getenv("CLICOLOR") != "0" &&
		os.Getenv("TERM") != "dumb" && term.IsTerminal(int(file.Fd())) &&
		enableANSI(file)
	return terminalUI{out: out, color: color}
}

func (u terminalUI) style(code, text string) string {
	if !u.color {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (u terminalUI) heading(context string) error {
	_, err := fmt.Fprintf(u.out, "\n  %s  %s\n\n",
		u.style("1;32", "mint"), u.style("1", context))
	return err
}

func (u terminalUI) step(number, title, detail string) error {
	_, err := fmt.Fprintf(u.out, "  %s  %s\n     %s\n\n",
		u.style("32", number), u.style("1", title), detail)
	return err
}

func (u terminalUI) status(marker, message string) error {
	_, err := fmt.Fprintf(u.out, "  %s  %s\n", u.style("32", marker), message)
	return err
}

func (u terminalUI) prompt(message string) error {
	_, err := fmt.Fprintf(u.out, "  %s %s", u.style("32", ">"), message)
	return err
}
