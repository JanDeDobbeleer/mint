package mint

import (
	"github.com/JanDeDobbeleer/mint/internal/vault"
)

// Repository tokens keep their original on-disk format and location; the vault package owns the encryption.

func tokenIdentity(repo repository) string {
	return vault.RepoIdentity(repo.Owner, repo.Name)
}

func tokenFilePath(dir string, repo repository) string {
	return vault.RepoTokenPath(dir, tokenIdentity(repo))
}

func writeEncryptedToken(path string, repo repository, token, password []byte, replace bool) error {
	return vault.WriteFile(path, token, password, vault.RepoTokenAD(tokenIdentity(repo)), replace)
}

func readEncryptedToken(path string, repo repository, password []byte) ([]byte, error) {
	return vault.ReadFile(path, password, vault.RepoTokenAD(tokenIdentity(repo)))
}
