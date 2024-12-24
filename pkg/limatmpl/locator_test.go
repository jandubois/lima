package limatmpl_test

import (
	"testing"

	"github.com/lima-vm/lima/pkg/limatmpl"
	"gotest.tools/v3/assert"
)

func TestBasePath(t *testing.T) {
	basePath, err := limatmpl.BasePath("/foo")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "/")

	basePath, err = limatmpl.BasePath("/foo/bar")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "/foo")

	basePath, err = limatmpl.BasePath("template://foo")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "template://")

	basePath, err = limatmpl.BasePath("template://foo/bar")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "template://foo")

	basePath, err = limatmpl.BasePath("http://host/foo")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "http://host")

	basePath, err = limatmpl.BasePath("http://host/foo/bar")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "http://host/foo")

	basePath, err = limatmpl.BasePath("file:///foo")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "file:///")

	basePath, err = limatmpl.BasePath("file:///foo/bar")
	assert.NilError(t, err)
	assert.Equal(t, basePath, "file:///foo")
}

func TestAbsPath(t *testing.T) {
	// If the locator is already an absolute path, it is returned unchanged (no extension appended either)
	actual, err := limatmpl.AbsPath("/foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "/foo")

	actual, err = limatmpl.AbsPath("template://foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "template://foo")

	actual, err = limatmpl.AbsPath("http://host/foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "http://host/foo")

	actual, err = limatmpl.AbsPath("file:///foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "file:///foo")

	// Can't have relative path when reading from STDIN
	_, err = limatmpl.AbsPath("foo", "-")
	assert.ErrorContains(t, err, "STDIN")

	// Relative paths must be underneath the basePath
	_, err = limatmpl.AbsPath("../foo", "/root")
	assert.ErrorContains(t, err, "'../'")

	// Relative paths are returned unchanged when basePath is empty
	actual, err = limatmpl.AbsPath("./foo", "")
	assert.NilError(t, err)
	assert.Equal(t, actual, "./foo")

	actual, err = limatmpl.AbsPath("foo", "")
	assert.NilError(t, err)
	assert.Equal(t, actual, "foo")

	// Check relative paths with all the supported schemes
	actual, err = limatmpl.AbsPath("./foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "/root/foo")

	actual, err = limatmpl.AbsPath("foo", "template://")
	assert.NilError(t, err)
	assert.Equal(t, actual, "template://foo")

	actual, err = limatmpl.AbsPath("bar", "template://foo")
	assert.NilError(t, err)
	assert.Equal(t, actual, "template://foo/bar")

	actual, err = limatmpl.AbsPath("foo", "http://host")
	assert.NilError(t, err)
	assert.Equal(t, actual, "http://host/foo")

	actual, err = limatmpl.AbsPath("bar", "http://host/foo")
	assert.NilError(t, err)
	assert.Equal(t, actual, "http://host/foo/bar")

	actual, err = limatmpl.AbsPath("foo", "file:///")
	assert.NilError(t, err)
	assert.Equal(t, actual, "file:///foo")

	actual, err = limatmpl.AbsPath("bar", "file:///foo")
	assert.NilError(t, err)
	assert.Equal(t, actual, "file:///foo/bar")
}
