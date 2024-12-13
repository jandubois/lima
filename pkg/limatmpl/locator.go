package limatmpl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/containerd/containerd/identifiers"
	"github.com/lima-vm/lima/pkg/ioutilx"
	"github.com/lima-vm/lima/pkg/limayaml"
	"github.com/lima-vm/lima/pkg/templatestore"
	"github.com/sirupsen/logrus"
)

// Unmarshal makes sure the tmpl.Config field is set. Any operation that modified
// tmpl.Bytes is expected to set tmpl.Config back to nil.
func (tmpl *Template) Unmarshal() error {
	if tmpl.Config == nil {
		tmpl.Config = &limayaml.LimaYAML{}
		if err := limayaml.Unmarshal(tmpl.Bytes, tmpl.Config, tmpl.Locator); err != nil {
			tmpl.Config = nil
			return err
		}
	}
	return nil
}

const yBytesLimit = 4 * 1024 * 1024 // 4MiB

func Read(ctx context.Context, name, locator, basePath string) (*Template, error) {
	locator, err := AbsPath(locator, basePath)
	if err != nil {
		return nil, err
	}

	tmpl := &Template{
		Name:    name,
		Locator: locator,
	}

	isTemplateURL, templateURL := SeemsTemplateURL(locator)
	switch {
	case isTemplateURL:
		// No need to use SecureJoin here. https://github.com/lima-vm/lima/pull/805#discussion_r853411702
		templateName := filepath.Join(templateURL.Host, templateURL.Path)
		logrus.Debugf("interpreting argument %q as a template name %q", locator, templateName)
		if tmpl.Name == "" {
			// e.g., templateName = "deprecated/centos-7.yaml" , tmpl.Name = "centos-7"
			tmpl.Name, err = InstNameFromYAMLPath(templateName)
			if err != nil {
				return nil, err
			}
		}
		tmpl.Bytes, err = templatestore.Read(templateName)
		if err != nil {
			return nil, err
		}
	case SeemsHTTPURL(locator):
		if tmpl.Name == "" {
			tmpl.Name, err = InstNameFromURL(locator)
			if err != nil {
				return nil, err
			}
		}
		logrus.Debugf("interpreting argument %q as a http url for instance %q", locator, tmpl.Name)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, locator, http.NoBody)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		tmpl.Bytes, err = ioutilx.ReadAtMaximum(resp.Body, yBytesLimit)
		if err != nil {
			return nil, err
		}
	case SeemsFileURL(locator):
		if tmpl.Name == "" {
			tmpl.Name, err = InstNameFromURL(locator)
			if err != nil {
				return nil, err
			}
		}
		logrus.Debugf("interpreting argument %q as a file url for instance %q", locator, tmpl.Name)
		filePath := strings.TrimPrefix(locator, "file://")
		if !filepath.IsAbs(filePath) {
			return nil, fmt.Errorf("file URL %q is not an absolute path", locator)
		}
		r, err := os.Open(filePath)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		tmpl.Bytes, err = ioutilx.ReadAtMaximum(r, yBytesLimit)
		if err != nil {
			return nil, err
		}
	case SeemsFilePath(locator):
		if tmpl.Name == "" {
			tmpl.Name, err = InstNameFromYAMLPath(locator)
			if err != nil {
				return nil, err
			}
		}
		logrus.Debugf("interpreting argument %q as a file path for instance %q", locator, tmpl.Name)
		r, err := os.Open(locator)
		if err != nil {
			return nil, err
		}
		defer r.Close()
		tmpl.Bytes, err = ioutilx.ReadAtMaximum(r, yBytesLimit)
		if err != nil {
			return nil, err
		}
	case locator == "-":
		tmpl.Bytes, err = io.ReadAll(os.Stdin)
		if err != nil {
			return nil, fmt.Errorf("unexpected error reading stdin: %w", err)
		}
	}
	return tmpl, nil
}

func SeemsTemplateURL(arg string) (bool, *url.URL) {
	u, err := url.Parse(arg)
	if err != nil {
		return false, u
	}
	return u.Scheme == "template", u
}

func SeemsHTTPURL(arg string) bool {
	u, err := url.Parse(arg)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return true
}

func SeemsFileURL(arg string) bool {
	u, err := url.Parse(arg)
	if err != nil {
		return false
	}
	return u.Scheme == "file"
}

func SeemsYAMLPath(arg string) bool {
	if strings.Contains(arg, "/") {
		return true
	}
	lower := strings.ToLower(arg)
	return strings.HasSuffix(lower, ".yml") || strings.HasSuffix(lower, ".yaml")
}

// SeemsFilePath returns true if arg either contains a slash or has a file extension that
// does not start with a digit. `script.sh` is a file path, `ubuntu-20.10` is not.
func SeemsFilePath(arg string) bool {
	if strings.Contains(arg, "/") {
		return true
	}
	ext := filepath.Ext(arg)
	return len(ext) > 1 && !unicode.IsDigit(rune(ext[1]))
}

func InstNameFromURL(urlStr string) (string, error) {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "", err
	}
	return InstNameFromYAMLPath(path.Base(u.Path))
}

func InstNameFromYAMLPath(yamlPath string) (string, error) {
	s := strings.ToLower(filepath.Base(yamlPath))
	s = strings.TrimSuffix(strings.TrimSuffix(s, ".yml"), ".yaml")
	// "." is allowed in instance names, but replaced to "-" for hostnames.
	// e.g., yaml: "ubuntu-24.04.yaml" , instance name: "ubuntu-24.04", hostname: "lima-ubuntu-24-04"
	if err := identifiers.Validate(s); err != nil {
		return "", fmt.Errorf("filename %q is invalid: %w", yamlPath, err)
	}
	return s, nil
}

// BasePath returns the locator without the filename part.
func BasePath(locator string) string {
	u, err := url.Parse(locator)
	if err != nil || u.Scheme == "" {
		return filepath.Dir(locator)
	}
	// filepath.Dir("") returns ".", which must be removed for url.JoinPath() to do the right thing later
	return u.Scheme + "://" + strings.TrimSuffix(filepath.Dir(filepath.Join(u.Host, u.Path)), ".")
}

// AbsPath either returns the locator directly, or combines it with the basePath if the locator is a relative path.
// If the relative locator doesn't already use the specified extension, then the extension is appended too.
func AbsPath(locator, basePath string) (string, error) {
	if basePath == "" {
		return locator, nil
	}
	u, err := url.Parse(locator)
	if (err == nil && u.Scheme != "") || filepath.IsAbs(locator) {
		return locator, nil
	}
	if basePath == "-" {
		return "", errors.New("can't use relative paths when reading template from STDIN")
	}
	if strings.Contains(locator, "../") {
		return "", fmt.Errorf("relative locator path %q must not contain '../' segments", locator)
	}
	u, err = url.Parse(basePath)
	if err != nil {
		return "", err
	}
	return u.JoinPath(locator).String(), nil
}
