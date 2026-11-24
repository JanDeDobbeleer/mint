package mint

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/JanDeDobbeleer/mint/internal/core"
	"github.com/JanDeDobbeleer/mint/internal/vault"
)

const credsHelp = `mint creds manages the named credentials in Mint's encrypted vault.

Usage:
  mint creds add NAME [--password-stdin]
  mint creds list
  mint creds rm NAME

add     Prompts for the value with hidden input and stores it encrypted. The master
        password comes from a hidden prompt, or from the first line of standard input
        with --password-stdin. The credential value is always typed at the prompt.
list    Shows credential names and when they changed. It never decrypts or prints values.
rm      Deletes a credential after you confirm.

Secret values are never accepted as flags or arguments: they would leak into process
lists and shell history. Known names:
`

// maxPEMLines bounds a pasted multi-line credential.
const maxPEMLines = 100

type credsEnv struct {
	prompts secretPrompter
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	dir     string
}

func (e credsEnv) usage() error {
	_, _ = fmt.Fprint(e.stderr, credsHelp)
	for _, spec := range vault.Known() {
		_, _ = fmt.Fprintf(e.stderr, "  %-27s %s\n", spec.Name, spec.Description)
	}
	return nil
}

// credsCommand runs `mint creds`. Arguments are parsed by hand so that a mistakenly supplied secret is never
// echoed back in an error message: option and argument text is not quoted.
func credsCommand(args []string, e credsEnv) error {
	if len(args) == 0 {
		return errors.New("missing creds command; run 'mint creds --help'")
	}
	switch args[0] {
	case "-h", "--help", "help":
		return e.usage()
	case "add":
		return e.add(args[1:])
	case "list":
		return e.list(args[1:])
	case "rm":
		return e.remove(args[1:])
	}
	return errors.New("unknown creds command; run 'mint creds --help'")
}

var errSecretArgument = errors.New("mint creds never accepts secret values as flags or arguments; enter them at the hidden prompt")

// parseCredsArgs returns the single credential name and whether --password-stdin was given.
// Any other option, or a second positional argument, is rejected without being quoted.
func parseCredsArgs(args []string, allowPasswordStdin bool) (name string, passwordStdin bool, err error) {
	var positional []string
	for _, arg := range args {
		switch {
		case arg == "--password-stdin" && allowPasswordStdin:
			passwordStdin = true
		case strings.HasPrefix(arg, "-"):
			return "", false, errSecretArgument
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 {
		if len(positional) > 1 {
			return "", false, errSecretArgument
		}
		return "", false, errors.New("a credential name is required; run 'mint creds --help'")
	}
	return positional[0], passwordStdin, nil
}

func (e credsEnv) add(args []string) error {
	name, passwordStdin, err := parseCredsArgs(args, true)
	if err != nil {
		return err
	}
	if err := vault.ValidName(name); err != nil {
		return err
	}
	spec, ok := vault.Lookup(name)
	if !ok {
		return errors.New("unknown credential name; run 'mint creds list' to see the supported names")
	}

	var source vault.Prompter = passwordPrompter{e.prompts}
	if passwordStdin {
		stdin := vault.NewStdinPrompter(e.stdin)
		defer stdin.Close()
		source = stdin
	}
	v, err := vault.Unlock(e.dir, source)
	if err != nil {
		return err
	}
	defer v.Close()

	replace := false
	if _, err := v.Get(name); err == nil {
		confirmed, err := e.prompts.confirm("Credential " + name + " already exists. Replace it? Type yes to replace [no]: ")
		if err != nil {
			return err
		}
		if !confirmed {
			return errors.New("the existing credential was kept")
		}
		replace = true
	} else if !errors.Is(err, vault.ErrNotFound) {
		// A damaged file is replaceable, but only after the same explicit confirmation.
		confirmed, confirmErr := e.prompts.confirm("The saved credential " + name + " cannot be read. Replace it? Type yes to replace [no]: ")
		if confirmErr != nil {
			return confirmErr
		}
		if !confirmed {
			return errors.New("the existing credential was kept")
		}
		replace = true
	}

	value, err := e.readValue(spec)
	if err != nil {
		return err
	}
	defer value.Destroy()
	if err := spec.Check(value.Reveal()); err != nil {
		return fmt.Errorf("invalid %s: %w", name, err)
	}
	if err := v.Put(name, value, replace); err != nil {
		return err
	}
	return newTerminalUI(e.stderr).status("+", "Saved credential "+name+".")
}

// readValue reads a credential with hidden input. PEM credentials are pasted line by line until their END line.
func (e credsEnv) readValue(spec vault.Spec) (core.Secret, error) {
	if !spec.PEM {
		input, err := e.prompts.password("Enter " + spec.Name + " (input hidden): ")
		if err != nil {
			return core.Secret{}, err
		}
		defer clear(input)
		return core.NewSecretString(strings.TrimSpace(string(input))), nil
	}
	var lines []byte
	defer func() { clear(lines) }()
	for i := range maxPEMLines {
		message := "Paste " + spec.Name + " (input hidden; finish with the END line): "
		if i > 0 {
			message = "  more (hidden): "
		}
		line, err := e.prompts.password(message)
		if err != nil {
			return core.Secret{}, err
		}
		trimmed := strings.TrimSpace(string(line))
		clear(line)
		if trimmed == "" {
			continue
		}
		lines = append(lines, trimmed...)
		lines = append(lines, '\n')
		if strings.HasPrefix(trimmed, "-----END ") {
			return core.NewSecret(lines), nil
		}
	}
	return core.Secret{}, errors.New("the pasted credential has no END line")
}

func (e credsEnv) list(args []string) error {
	if len(args) != 0 {
		return errors.New("creds list takes no arguments")
	}
	stored, err := vault.List(e.dir)
	if err != nil {
		return err
	}
	byName := make(map[string]vault.Info, len(stored))
	for _, info := range stored {
		byName[info.Name] = info
	}
	if _, err := fmt.Fprintf(e.stdout, "%-27s %-8s %s\n", "NAME", "STATUS", "UPDATED"); err != nil {
		return err
	}
	for _, spec := range vault.Known() {
		line := fmt.Sprintf("%-27s %-8s %s\n", spec.Name, "not set", "-")
		if info, ok := byName[spec.Name]; ok {
			line = fmt.Sprintf("%-27s %-8s %s\n", spec.Name, "stored", info.Updated.Local().Format("2006-01-02 15:04"))
		}
		if _, err := io.WriteString(e.stdout, line); err != nil {
			return err
		}
	}
	for _, info := range stored {
		if info.Known {
			continue
		}
		if _, err := fmt.Fprintf(e.stdout, "%-27s %-8s %s\n", info.Name, "unknown", info.Updated.Local().Format("2006-01-02 15:04")); err != nil {
			return err
		}
	}
	return nil
}

func (e credsEnv) remove(args []string) error {
	name, _, err := parseCredsArgs(args, false)
	if err != nil {
		return err
	}
	if err := vault.ValidName(name); err != nil {
		return err
	}
	stored, err := vault.List(e.dir)
	if err != nil {
		return err
	}
	found := false
	for _, info := range stored {
		found = found || info.Name == name
	}
	if !found {
		return fmt.Errorf("%w: %s", vault.ErrNotFound, name)
	}
	confirmed, err := e.prompts.confirm("Delete credential " + name + "? This cannot be undone. Type yes to delete [no]: ")
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("the credential was kept")
	}
	if err := vault.DeleteCredential(e.dir, name); err != nil {
		return err
	}
	return newTerminalUI(e.stderr).status("+", "Removed credential "+name+".")
}

func runCreds(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	dir, err := configDirectory()
	if err != nil {
		return err
	}
	return credsCommand(args, credsEnv{
		prompts: terminalPrompter{stdin: stdin, stderr: stderr},
		stdin:   stdin, stdout: stdout, stderr: stderr, dir: dir,
	})
}
