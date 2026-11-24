package config

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// secretPrefixes are credential formats that must never appear in a config file.
var secretPrefixes = []struct{ prefix, kind string }{
	{"ghp_", "a GitHub personal access token"},
	{"gho_", "a GitHub OAuth token"},
	{"ghu_", "a GitHub user token"},
	{"ghs_", "a GitHub installation token"},
	{"ghr_", "a GitHub refresh token"},
	{"github_pat_", "a GitHub fine-grained token"},
	{"sk-ant-", "an Anthropic API key"},
	{"xoxb-", "a Slack token"},
	{"xoxp-", "a Slack token"},
	{"AKIA", "an AWS access key ID"},
}

var tokenShape = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{32,}$`)

// scanSecrets walks the parsed document and rejects any value that looks like a credential. It reports the line
// and the kind but never the value. Config refers to credentials by vault name, so a hit is always a mistake.
func scanSecrets(node *yaml.Node) []error {
	var problems []error
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		switch n.Kind {
		case yaml.ScalarNode:
			if kind := secretKind(n.Value); kind != "" {
				problems = append(problems, fmt.Errorf("line %d: the value looks like %s; reference a vault credential by name instead", n.Line, kind))
			}
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, child := range n.Content {
				walk(child)
			}
		case yaml.MappingNode:
			// Values only: keys are schema words or operator-chosen names.
			for i := 1; i < len(n.Content); i += 2 {
				walk(n.Content[i])
			}
		}
	}
	walk(node)
	return problems
}

func secretKind(value string) string {
	if strings.Contains(value, "-----BEGIN ") {
		return "a PEM block"
	}
	for _, s := range secretPrefixes {
		if strings.HasPrefix(value, s.prefix) && len(value) >= len(s.prefix)+8 && !strings.ContainsAny(value, " \t") {
			return s.kind
		}
	}
	if tokenShape.MatchString(value) && entropy(value) > 4.3 {
		return "a high-entropy token"
	}
	return ""
}

// entropy is the Shannon entropy in bits per character.
func entropy(s string) float64 {
	counts := map[rune]int{}
	for _, r := range s {
		counts[r]++
	}
	total := float64(len([]rune(s)))
	var h float64
	for _, c := range counts {
		p := float64(c) / total
		h -= p * math.Log2(p)
	}
	return h
}
