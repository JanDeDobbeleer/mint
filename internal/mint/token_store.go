package mint

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	tokenFileHeader = "MINTPAT1"
	tokenSaltSize   = 16
	tokenKeySize    = 32
	tokenIterations = 600_000
	maxTokenFile    = 8 * 1024
)

func tokenIdentity(repo repository) string {
	return strings.ToLower(repo.Owner + "/" + repo.Name)
}

func tokenFilePath(dir string, repo repository) string {
	hash := sha256.Sum256([]byte(tokenIdentity(repo)))
	return filepath.Join(dir, "tokens", hex.EncodeToString(hash[:])+".enc")
}

func writeEncryptedToken(path string, repo repository, token, password []byte, replace bool) error {
	if len(password) == 0 {
		return errors.New("token-store passphrase cannot be empty")
	}
	data, err := encryptToken(repo, token, password)
	if err != nil {
		return err
	}
	defer clear(data)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the encrypted token directory")
	}
	if replace {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot inspect the saved repository token")
		}
		if err == nil && !info.Mode().IsRegular() {
			return errors.New("saved repository token must be a regular file")
		}
		file, err := os.CreateTemp(dir, ".token-*.tmp")
		if err != nil {
			return errors.New("cannot stage the encrypted repository token")
		}
		temp := file.Name()
		defer func() { _ = os.Remove(temp) }()
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return errors.New("cannot protect the staged repository token")
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return errors.New("cannot write the encrypted repository token")
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return errors.New("cannot persist the encrypted repository token")
		}
		if err := file.Close(); err != nil {
			return errors.New("cannot finish writing the encrypted repository token")
		}
		if err := os.Rename(temp, path); err != nil {
			return errors.New("cannot replace the encrypted repository token")
		}
		return nil
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create the encrypted repository token")
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot write the encrypted repository token")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot persist the encrypted repository token")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return errors.New("cannot finish writing the encrypted repository token")
	}
	return nil
}

func readEncryptedToken(path string, repo repository, password []byte) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxTokenFile {
		return nil, errors.New("invalid encrypted repository token file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read encrypted repository token")
	}
	defer clear(data)
	if len(data) < len(tokenFileHeader)+tokenSaltSize+12+16 || string(data[:len(tokenFileHeader)]) != tokenFileHeader {
		return nil, errors.New("invalid encrypted repository token format")
	}
	offset := len(tokenFileHeader)
	salt := data[offset : offset+tokenSaltSize]
	offset += tokenSaltSize
	key := pbkdf2.Key(password, salt, tokenIterations, tokenKeySize, sha256.New)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("cannot initialize repository token decryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("cannot initialize repository token decryption")
	}
	if len(data) < offset+aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid encrypted repository token format")
	}
	nonce := data[offset : offset+aead.NonceSize()]
	ciphertext := data[offset+aead.NonceSize():]
	token, err := aead.Open(nil, nonce, ciphertext, tokenAssociatedData(repo))
	if err != nil {
		return nil, errors.New("cannot decrypt repository token")
	}
	return token, nil
}

func encryptToken(repo repository, token, password []byte) ([]byte, error) {
	if len(password) == 0 {
		return nil, errors.New("token-store passphrase cannot be empty")
	}
	salt := make([]byte, tokenSaltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, errors.New("cannot generate repository token encryption salt")
	}
	key := pbkdf2.Key(password, salt, tokenIterations, tokenKeySize, sha256.New)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("cannot initialize repository token encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("cannot initialize repository token encryption")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("cannot generate repository token encryption nonce")
	}
	result := make([]byte, 0, len(tokenFileHeader)+len(salt)+len(nonce)+len(token)+aead.Overhead())
	result = append(result, tokenFileHeader...)
	result = append(result, salt...)
	result = append(result, nonce...)
	result = aead.Seal(result, nonce, token, tokenAssociatedData(repo))
	return result, nil
}

func tokenAssociatedData(repo repository) []byte {
	return []byte(tokenFileHeader + "\x00" + tokenIdentity(repo))
}
