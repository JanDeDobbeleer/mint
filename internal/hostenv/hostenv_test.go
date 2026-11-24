package hostenv

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterKeepsOnlyAllowlistedVariablesAndAppliesSortedOverrides(t *testing.T) {
	got := Filter([]string{
		"PATH=/bin", "path_lower_is_not_a_name", "GH_TOKEN=leak", "Lang=C", "LC_ALL=C", "AWS_SECRET_ACCESS_KEY=x",
		"SYSTEMROOT=C:\\Windows", "NODE_OPTIONS=--require=x", "TEMP=old", "NOEQUALS",
	}, map[string]string{"ZED": "1", "TEMP": "new", "ALPHA": "2"})
	assert.Equal(t, []string{"PATH=/bin", "Lang=C", "LC_ALL=C", "SYSTEMROOT=C:\\Windows", "ALPHA=2", "TEMP=new", "ZED=1"}, got)
}

func TestFilterWithoutOverrides(t *testing.T) {
	assert.Equal(t, []string{"HOME=/h"}, Filter([]string{"HOME=/h", "COPILOT_GITHUB_TOKEN=secret"}, nil))
}
