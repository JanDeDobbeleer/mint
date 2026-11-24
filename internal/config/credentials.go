package config

import "github.com/JanDeDobbeleer/mint/internal/vault"

// credentialKnown reports whether name is one of the vault's defined credentials. Config holds names, never values.
func credentialKnown(name string) bool { return vault.IsKnown(name) }
