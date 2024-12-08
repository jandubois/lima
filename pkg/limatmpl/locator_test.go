package limatmpl

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestBasePath(t *testing.T) {
	assert.Equal(t, BasePath("/foo"), "/")
	assert.Equal(t, BasePath("/foo/bar"), "/foo")
	assert.Equal(t, BasePath("template://foo"), "template://")
	assert.Equal(t, BasePath("template://foo/bar"), "template://foo")
	assert.Equal(t, BasePath("http://host/foo"), "http://host")
	assert.Equal(t, BasePath("http://host/foo/bar"), "http://host/foo")
	assert.Equal(t, BasePath("file:///foo"), "file:///")
	assert.Equal(t, BasePath("file:///foo/bar"), "file:///foo")
}

func TestAbsPath(t *testing.T) {
	// If the locator is already an absolute path, it is returned unchanged (no extension appended either)
	actual, err := AbsPath("/foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "/foo")

	actual, err = AbsPath("template://foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "template://foo")

	actual, err = AbsPath("http://host/foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "http://host/foo")

	actual, err = AbsPath("file:///foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "file:///foo")

	// Can't have relative path when reading from STDIN
	_, err = AbsPath("foo", "-")
	assert.ErrorContains(t, err, "STDIN")

	// Relative paths must be underneath the basePath
	_, err = AbsPath("../foo", "/root")
	assert.ErrorContains(t, err, "'../'")

	// Relative paths are returned unchanged when basePath is empty
	actual, err = AbsPath("./foo", "")
	assert.NilError(t, err)
	assert.Equal(t, actual, "./foo")

	actual, err = AbsPath("foo", "")
	assert.NilError(t, err)
	assert.Equal(t, actual, "foo")

	// Check relative paths with all the supported schemes
	actual, err = AbsPath("./foo", "/root")
	assert.NilError(t, err)
	assert.Equal(t, actual, "/root/foo")

	actual, err = AbsPath("foo", "template://")
	assert.NilError(t, err)
	assert.Equal(t, actual, "template://foo")

	actual, err = AbsPath("bar", "template://foo")
	assert.NilError(t, err)
	assert.Equal(t, actual, "template://foo/bar")

	actual, err = AbsPath("foo", "http://host")
	assert.NilError(t, err)
	assert.Equal(t, actual, "http://host/foo")

	actual, err = AbsPath("bar", "http://host/foo")
	assert.NilError(t, err)
	assert.Equal(t, actual, "http://host/foo/bar")

	actual, err = AbsPath("foo", "file:///")
	assert.NilError(t, err)
	assert.Equal(t, actual, "file:///foo")

	actual, err = AbsPath("bar", "file:///foo")
	assert.NilError(t, err)
	assert.Equal(t, actual, "file:///foo/bar")
}
