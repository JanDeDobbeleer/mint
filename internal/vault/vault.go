package vault

import (
	"bytes"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/JanDeDobbeleer/mint/internal/core"
)

const (
	masterFile     = "master.enc"
	credentialsDir = "credentials"
	tokensDir      = "tokens"
	masterContext  = "mint:master-key:v1"

	// StateSigningInfo is the HKDF context of the key that signs run-state comments (design 6).
	StateSigningInfo = "mint:state-hmac:v1"
)

// ErrNotFound reports that a credential does not exist.
var ErrNotFound = errors.New("credential not found")

// Prompter supplies the master password. Implementations must return a buffer the vault may zeroize.
type Prompter interface {
	Password(message string) ([]byte, error)
}

// Vault is an unlocked credential store rooted at one directory. All methods are safe for concurrent use.
type Vault struct {
	dir string
	key []byte
	mu  sync.Mutex
}

// Unlock opens the vault in dir, creating the store key (and asking to confirm a new master password) on first use.
// The caller must Close the vault.
func Unlock(dir string, p Prompter) (*Vault, error) {
	key, err := UnlockKey(dir, p)
	if err != nil {
		return nil, err
	}
	return &Vault{dir: dir, key: key}, nil
}

// UnlockKey returns the raw store key, creating it on first use. A wrong password never changes any file.
func UnlockKey(dir string, p Prompter) ([]byte, error) {
	path := filepath.Join(dir, masterFile)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return initialize(path, p)
	}
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("cannot inspect the Mint master key")
	}
	password, err := p.Password("Mint master password: ")
	if err != nil {
		return nil, err
	}
	defer clear(password)
	key, err := ReadFile(path, password, []byte(masterContext))
	if err != nil || len(key) != keySize {
		clear(key)
		return nil, errors.New("cannot unlock the token store; check your master password")
	}
	return key, nil
}

func initialize(path string, p Prompter) ([]byte, error) {
	password, err := p.Password("Create Mint master password: ")
	if err != nil {
		return nil, err
	}
	defer clear(password)
	if len(password) == 0 {
		return nil, errors.New("master password cannot be empty")
	}
	confirmation, err := p.Password("Confirm Mint master password: ")
	if err != nil {
		return nil, err
	}
	defer clear(confirmation)
	if !bytes.Equal(password, confirmation) {
		return nil, errors.New("master passwords do not match")
	}
	key := make([]byte, keySize)
	if _, err := rand.Read(key); err != nil {
		clear(key)
		return nil, errors.New("cannot generate the Mint master key")
	}
	if err := WriteFile(path, key, password, []byte(masterContext), false); err != nil {
		clear(key)
		return nil, errors.New("cannot save the encrypted Mint master key")
	}
	return key, nil
}

// Close zeroizes the store key. The vault is unusable afterwards.
func (v *Vault) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	clear(v.key)
	v.key = nil
}

func (v *Vault) unlocked() error {
	if v.key == nil {
		return errors.New("the vault is locked")
	}
	return nil
}

// DeriveKey derives an independent key from the store key with HKDF-SHA256. Distinct info strings give unrelated keys.
func (v *Vault) DeriveKey(info string, size int) (core.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.unlocked(); err != nil {
		return core.Secret{}, err
	}
	key, err := hkdf.Key(sha256.New, v.key, nil, info, size)
	if err != nil {
		return core.Secret{}, errors.New("cannot derive a key")
	}
	defer clear(key)
	return core.NewSecret(key), nil
}

// StateSigningKey is the HMAC key for run-state comments.
func (v *Vault) StateSigningKey() (core.Secret, error) { return v.DeriveKey(StateSigningInfo, 32) }

// --- named credentials ---

var (
	nameRule     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,62}[a-z0-9]$`)
	windowsNames = regexp.MustCompile(`^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)
)

// ValidName reports whether name can be used as a credential name: lower-case letters, digits and hyphens.
// The rule also keeps names safe as file names on every platform.
func ValidName(name string) error {
	if !nameRule.MatchString(name) || strings.Contains(name, "--") || windowsNames.MatchString(name) {
		return errors.New("credential names use lower-case letters, digits and single hyphens, and start with a letter")
	}
	return nil
}

func credentialAD(name string) []byte { return []byte(fileHeader + "\x00credential\x00" + name) }

func (v *Vault) credentialPath(name string) string {
	return filepath.Join(v.dir, credentialsDir, name+".enc")
}

// Put stores value under name. Without replace it fails if the credential exists.
func (v *Vault) Put(name string, value core.Secret, replace bool) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if value.IsZero() {
		return errors.New("a credential cannot be empty")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.unlocked(); err != nil {
		return err
	}
	return v.putLocked(name, value, replace)
}

func (v *Vault) putLocked(name string, value core.Secret, replace bool) error {
	plain := []byte(value.Reveal())
	defer clear(plain)
	if err := WriteFile(v.credentialPath(name), plain, v.key, credentialAD(name), replace); err != nil {
		if !replace {
			if _, statErr := os.Lstat(v.credentialPath(name)); statErr == nil {
				return fmt.Errorf("credential %q already exists", name)
			}
		}
		return err
	}
	return nil
}

// Get decrypts the credential name. A missing credential returns ErrNotFound.
func (v *Vault) Get(name string) (core.Secret, error) {
	if err := ValidName(name); err != nil {
		return core.Secret{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.unlocked(); err != nil {
		return core.Secret{}, err
	}
	return v.getLocked(name)
}

func (v *Vault) getLocked(name string) (core.Secret, error) {
	path := v.credentialPath(name)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return core.Secret{}, fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	plain, err := ReadFile(path, v.key, credentialAD(name))
	if err != nil {
		return core.Secret{}, err
	}
	defer clear(plain)
	return core.NewSecret(plain), nil
}

// Rotate is the runtime write path: it reads the credential, passes it to update and atomically re-encrypts the
// result in place while holding the vault lock, so concurrent rotations never lose an update. If update fails,
// nothing is written and the old value stays. OAuth refresh tokens, which rotate on every use, are the main caller.
func (v *Vault) Rotate(name string, update func(current core.Secret) (core.Secret, error)) error {
	if err := ValidName(name); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.unlocked(); err != nil {
		return err
	}
	current, err := v.getLocked(name)
	if err != nil {
		return err
	}
	defer current.Destroy()
	next, err := update(current)
	if err != nil {
		return err
	}
	defer next.Destroy()
	if next.IsZero() {
		return errors.New("a credential cannot be empty")
	}
	return v.putLocked(name, next, true)
}

// Delete removes the credential name.
func (v *Vault) Delete(name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return DeleteCredential(v.dir, name)
}

// Info describes a stored credential without revealing it.
type Info struct {
	Updated time.Time
	Name    string
	Known   bool
}

// List returns the stored credentials in name order. It reads only the directory, never decrypts, and needs no password.
func List(dir string) ([]Info, error) {
	entries, err := os.ReadDir(filepath.Join(dir, credentialsDir))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot list credentials")
	}
	var result []Info
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".enc")
		if !ok || entry.IsDir() || ValidName(name) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		result = append(result, Info{Name: name, Updated: info.ModTime(), Known: IsKnown(name)})
	}
	slices.SortFunc(result, func(a, b Info) int { return strings.Compare(a.Name, b.Name) })
	return result, nil
}

// DeleteCredential removes a credential file without unlocking the vault.
func DeleteCredential(dir, name string) error {
	if err := ValidName(name); err != nil {
		return err
	}
	err := os.Remove(filepath.Join(dir, credentialsDir, name+".enc"))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	if err != nil {
		return errors.New("cannot remove the credential")
	}
	return nil
}

// --- repository tokens (the original MINTPAT1 layout) ---

// RepoIdentity is the lower-case owner/name that binds a repository token.
func RepoIdentity(owner, name string) string { return strings.ToLower(owner + "/" + name) }

// RepoTokenPath is where the token for a repository lives.
func RepoTokenPath(dir, identity string) string {
	hash := sha256.Sum256([]byte(identity))
	return filepath.Join(dir, tokensDir, hex.EncodeToString(hash[:])+".enc")
}

// RepoTokenAD is the associated data that binds a token file to its repository.
func RepoTokenAD(identity string) []byte { return []byte(fileHeader + "\x00" + identity) }

// PutRepoToken stores a repository token.
func (v *Vault) PutRepoToken(identity string, token core.Secret, replace bool) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.unlocked(); err != nil {
		return err
	}
	plain := []byte(token.Reveal())
	defer clear(plain)
	return WriteFile(RepoTokenPath(v.dir, identity), plain, v.key, RepoTokenAD(identity), replace)
}

// GetRepoToken decrypts a repository token.
func (v *Vault) GetRepoToken(identity string) (core.Secret, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.unlocked(); err != nil {
		return core.Secret{}, err
	}
	plain, err := ReadFile(RepoTokenPath(v.dir, identity), v.key, RepoTokenAD(identity))
	if err != nil {
		return core.Secret{}, err
	}
	defer clear(plain)
	return core.NewSecret(plain), nil
}
