package mint

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
)

const masterKeyContext = "mint:master-key:v1"

func unlockTokenStore(dir string, prompts secretPrompter) ([]byte, error) {
	path := filepath.Join(dir, "master.enc")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return initializeTokenStore(path, prompts)
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("cannot inspect the Mint master key")
	}
	password, err := prompts.password("Mint master password: ")
	if err != nil {
		return nil, err
	}
	defer clear(password)
	key, err := readEncryptedSecret(path, password, []byte(masterKeyContext))
	if err != nil || len(key) != tokenKeySize {
		clear(key)
		return nil, errors.New("cannot unlock the token store; check your master password")
	}
	return key, nil
}

func initializeTokenStore(path string, prompts secretPrompter) ([]byte, error) {
	password, err := prompts.password("Create Mint master password: ")
	if err != nil {
		return nil, err
	}
	defer clear(password)
	if len(password) == 0 {
		return nil, errors.New("master password cannot be empty")
	}
	confirmation, err := prompts.password("Confirm Mint master password: ")
	if err != nil {
		return nil, err
	}
	defer clear(confirmation)
	if !bytes.Equal(password, confirmation) {
		return nil, errors.New("master passwords do not match")
	}
	key := make([]byte, tokenKeySize)
	if _, err := rand.Read(key); err != nil {
		clear(key)
		return nil, errors.New("cannot generate the Mint master key")
	}
	if err := writeEncryptedSecret(path, key, password, []byte(masterKeyContext), false); err != nil {
		clear(key)
		return nil, errors.New("cannot save the encrypted Mint master key")
	}
	return key, nil
}
