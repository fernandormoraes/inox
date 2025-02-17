package binary

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetLatestReleases(t *testing.T) {

	if testing.Short() {
		return
	}

	tags, err := FetchTags()
	if !assert.NoError(t, err) {
		return
	}

	releases, err := GetLatestNReleases(tags, 2)
	if !assert.NoError(t, err) {
		return
	}

	if !assert.LessOrEqual(t, len(releases), 2) {
		return
	}

	if len(releases) > 0 {
		assert.NotEmpty(t, releases[0].TagName)
		assert.NotEmpty(t, releases[0].Name)
	}

	if len(releases) > 1 {
		assert.NotEmpty(t, releases[1].TagName)
		assert.NotEmpty(t, releases[1].Name)
	}
}
