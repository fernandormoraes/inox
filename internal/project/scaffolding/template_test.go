package scaffolding

import (
	"testing"

	"github.com/go-git/go-billy/v5/util"
	"github.com/inoxlang/inox/internal/globals/fs_ns"
	"github.com/stretchr/testify/assert"
)

func TestWriteTemplate(t *testing.T) {

	fls := fs_ns.NewMemFilesystem(1_000_000)

	if !assert.NoError(t, WriteTemplate("web-app-min", fls)) {
		return
	}

	_, err := fls.Stat("/main.ix")
	if !assert.NoError(t, err) {
		return
	}

	content, err := util.ReadFile(fls, "/static/htmx.min.js")

	if !assert.NoError(t, err) {
		return
	}

	assert.Equal(t, FULL_HTMX_MIN_JS, string(content))
	assert.NotEmpty(t, FULL_HTMX_MIN_JS)

	content, err = util.ReadFile(fls, "/static/base.css")

	if !assert.NoError(t, err) {
		return
	}

	assert.Equal(t, BASE_CSS_STYLESHEET, string(content))
	assert.NotEmpty(t, BASE_CSS_STYLESHEET)
}
