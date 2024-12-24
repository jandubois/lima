package limatmpl_test

import (
	"strings"
	"testing"

	"github.com/lima-vm/lima/pkg/limatmpl"
	"gotest.tools/v3/assert"
)

type copyTestCase struct {
	description string
	locator     string
	template    string
	expected    string
}

var copyTestCases = []copyTestCase{
	{
		"Template without basedOn or script file",
		"template://foo",
		`foo: bar`,
		`foo: bar`,
	},
	{
		"Single string base template",
		"template://foo",
		`basedOn: bar.yaml`,
		`basedOn: template://bar.yaml`,
	},
	{
		"Single string base template",
		"template://foo",
		`basedOn: [bar.yaml]`,
		`basedOn: ['template://bar.yaml']`,
	},
	{
		"Single string base template",
		"template://foo",
		`
basedOn:
- bar.yaml
`,
		`
basedOn:
- template://bar.yaml`,
	},
	{
		"Single string base template",
		"template://foo",
		`
basedOn:
- bar.yaml
- template://my
- https://example.com/my.yaml
- baz.yaml
`,
		`
basedOn:
- template://bar.yaml
- template://my
- https://example.com/my.yaml
- template://baz.yaml
`,
	},
}

func TestCopy(t *testing.T) {
	for _, tc := range copyTestCases {
		t.Run(tc.description, func(t *testing.T) { RunCopyTest(t, tc) })
	}
}

func RunCopyTest(t *testing.T, tc copyTestCase) {
	tmpl := &limatmpl.Template{
		Bytes:   []byte(strings.TrimSpace(tc.template)),
		Locator: tc.locator,
	}
	err := tmpl.Copy()
	assert.NilError(t, err, tc.description)

	actual := strings.TrimSpace(string(tmpl.Bytes))
	expected := strings.TrimSpace(tc.expected)
	assert.Equal(t, actual, expected, tc.description)
}
