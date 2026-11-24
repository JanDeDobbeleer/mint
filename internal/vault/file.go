// Package vault stores Mint's secrets: one random store key sealed under the operator's master password
// (master.enc), and independent encrypted files for each named credential and each repository token.
//
// Every file uses the same layout as the original repository-token store, so existing files keep working:
//
//	"MINTPAT1" | salt (16) | AES-GCM nonce (12) | ciphertext+tag
//
// The AES key is PBKDF2-HMAC-SHA256(password, salt, 600000). For credential and token files the "password"
// is the 32-byte store key. The AES-GCM associated data binds each file to what it holds, so a file copied
// or renamed to another credential, or to another repository, fails to decrypt.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/crypto/pbkdf2"
)

const (
	fileHeader  = "MINTPAT1"
	saltSize    = 16
	keySize     = 32
	maxFileSize = 8 * 1024
)

// kdfIterations is a variable only so tests can lower it; files written with a different count cannot be read back.
var kdfIterations = 600_000

// Seal encrypts secret under password, bound to associatedData.
func Seal(secret, password, associatedData []byte) ([]byte, error) {
	if len(password) == 0 {
		return nil, errors.New("vault passphrase cannot be empty")
	}
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, errors.New("cannot generate encryption salt")
	}
	key := pbkdf2.Key(password, salt, kdfIterations, keySize, sha256.New)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("cannot initialize encryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("cannot initialize encryption")
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, errors.New("cannot generate encryption nonce")
	}
	result := make([]byte, 0, len(fileHeader)+len(salt)+len(nonce)+len(secret)+aead.Overhead())
	result = append(result, fileHeader...)
	result = append(result, salt...)
	result = append(result, nonce...)
	return aead.Seal(result, nonce, secret, associatedData), nil
}

// Open decrypts data produced by Seal. Any failure, including wrong associated data, returns the same error.
func Open(data, password, associatedData []byte) ([]byte, error) {
	if len(data) < len(fileHeader)+saltSize+12+16 || string(data[:len(fileHeader)]) != fileHeader {
		return nil, errors.New("invalid encrypted file format")
	}
	offset := len(fileHeader)
	salt := data[offset : offset+saltSize]
	offset += saltSize
	key := pbkdf2.Key(password, salt, kdfIterations, keySize, sha256.New)
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("cannot initialize decryption")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("cannot initialize decryption")
	}
	if len(data) < offset+aead.NonceSize()+aead.Overhead() {
		return nil, errors.New("invalid encrypted file format")
	}
	nonce := data[offset : offset+aead.NonceSize()]
	plain, err := aead.Open(nil, nonce, data[offset+aead.NonceSize():], associatedData)
	if err != nil {
		return nil, errors.New("cannot decrypt credential")
	}
	return plain, nil
}

// WriteFile seals secret and stores it at path with mode 0600. With replace false it fails if the file exists.
// With replace true the old file is swapped for the new one atomically: readers see the old or the new content,
// and a failure at any step leaves the old file untouched.
func WriteFile(path string, secret, password, associatedData []byte, replace bool) error {
	data, err := Seal(secret, password, associatedData)
	if err != nil {
		return err
	}
	defer clear(data)

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the encrypted credential directory")
	}
	if replace {
		info, err := os.Lstat(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.New("cannot inspect the saved credential")
		}
		if err == nil && !info.Mode().IsRegular() {
			return errors.New("saved credential must be a regular file")
		}
		file, err := os.CreateTemp(dir, ".credential-*.tmp")
		if err != nil {
			return errors.New("cannot stage the encrypted credential")
		}
		temp := file.Name()
		defer func() { _ = os.Remove(temp) }()
		if err := file.Chmod(0o600); err != nil {
			_ = file.Close()
			return errors.New("cannot protect the staged credential")
		}
		if _, err := file.Write(data); err != nil {
			_ = file.Close()
			return errors.New("cannot write the encrypted credential")
		}
		if err := file.Sync(); err != nil {
			_ = file.Close()
			return errors.New("cannot persist the encrypted credential")
		}
		if err := file.Close(); err != nil {
			return errors.New("cannot finish writing the encrypted credential")
		}
		if err := os.Rename(temp, path); err != nil {
			return errors.New("cannot replace the encrypted credential")
		}
		return nil
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return errors.New("cannot create the encrypted credential")
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot write the encrypted credential")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return errors.New("cannot persist the encrypted credential")
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return errors.New("cannot finish writing the encrypted credential")
	}
	return nil
}

// ReadFile reads and decrypts the file at path. The caller must clear the result.
func ReadFile(path string, password, associatedData []byte) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileSize {
		return nil, errors.New("invalid encrypted credential file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("cannot read encrypted credential")
	}
	defer clear(data)
	return Open(data, password, associatedData)
}
