package mint

import (
	"github.com/JanDeDobbeleer/mint/internal/vault"
)

// passwordPrompter adapts a secretPrompter to the vault's Prompter.
type passwordPrompter struct{ secretPrompter }

func (p passwordPrompter) Password(message string) ([]byte, error) { return p.password(message) }

func unlockTokenStore(dir string, prompts secretPrompter) ([]byte, error) {
	return vault.UnlockKey(dir, passwordPrompter{prompts})
}
