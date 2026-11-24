package vault

import (
	"encoding/pem"
	"errors"
	"slices"
	"strings"
)

// Credential names from design section 8. Config files refer to credentials by these names only.
const (
	GitHubAppPrivateKey    = "github-app-private-key" // the App and installation IDs are non-secret config
	GitHubOperatorPAT      = "github-operator-pat"    // interactive launcher and `mint run` only
	CopilotToken           = "copilot-token"
	EmailIMAPPassword      = "email-imap-password"
	EmailOAuthRefresh      = "email-oauth-refresh-token"
	EmailOAuthClientSecret = "email-oauth-client-secret"
	WhatsAppToken          = "whatsapp-token"
	WhatsAppAppSecret      = "whatsapp-app-secret"
	WhatsAppVerifyToken    = "whatsapp-verify-token"
	WebhookSecret          = "webhook-secret"
)

// Spec describes a known credential: what it is and how a value is entered and checked.
type Spec struct {
	Validate    func(value string) error
	Name        string
	Description string
	// PEM credentials are entered as several lines and must hold one PEM block.
	PEM bool
}

var known = []Spec{
	{Name: GitHubAppPrivateKey, Description: "GitHub App private key (PEM)", PEM: true, Validate: validatePEM},
	{Name: GitHubOperatorPAT, Description: "operator fine-grained PAT, for the interactive launcher and `mint run`", Validate: validateFineGrainedPAT},
	{Name: CopilotToken, Description: "token the Copilot CLI authenticates with", Validate: validateCopilotToken},
	{Name: EmailIMAPPassword, Description: "IMAP password or app password"},
	{Name: EmailOAuthRefresh, Description: "email OAuth refresh token (rotates at runtime)"},
	{Name: EmailOAuthClientSecret, Description: "email OAuth client secret"},
	{Name: WhatsAppToken, Description: "WhatsApp Cloud API access token"},
	{Name: WhatsAppAppSecret, Description: "WhatsApp app secret (webhook signature key)"},
	{Name: WhatsAppVerifyToken, Description: "WhatsApp webhook verify token"},
	{Name: WebhookSecret, Description: "shared secret for signed webhooks"},
}

// Known returns the credential names Mint defines, in documentation order.
func Known() []Spec { return slices.Clone(known) }

// Lookup returns the spec for a known credential name.
func Lookup(name string) (Spec, bool) {
	for _, spec := range known {
		if spec.Name == name {
			return spec, true
		}
	}
	return Spec{}, false
}

// IsKnown reports whether name is one of Mint's defined credentials.
func IsKnown(name string) bool { _, ok := Lookup(name); return ok }

// Check validates a value entered for spec. Single-line credentials may not contain whitespace or control characters.
func (s Spec) Check(value string) error {
	if value == "" {
		return errors.New("a credential cannot be empty")
	}
	if !s.PEM && strings.ContainsAny(value, " \t\r\n\x00") {
		return errors.New("credential values cannot contain whitespace")
	}
	if len(value) > maxFileSize/2 {
		return errors.New("credential value is too large")
	}
	if s.Validate != nil {
		return s.Validate(value)
	}
	return nil
}

func validatePEM(value string) error {
	block, rest := pem.Decode([]byte(value))
	if block == nil || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("expected exactly one PEM block")
	}
	if !strings.HasSuffix(block.Type, "PRIVATE KEY") {
		return errors.New("expected a PEM private key")
	}
	return nil
}

func validateFineGrainedPAT(value string) error {
	if !strings.HasPrefix(value, "github_pat_") {
		return errors.New("expected a fine-grained token (github_pat_...)")
	}
	return nil
}

func validateCopilotToken(value string) error {
	if strings.HasPrefix(value, "ghp_") {
		return errors.New("classic personal access tokens are rejected by Copilot; use a fine-grained token with the Copilot Requests permission")
	}
	return nil
}
