package limatmpl

import "fmt"

// Copy will replace all relative template locators with absolute ones, so this template
// can be stored anywhere and still reference the same base templates and files.
func (tmpl *Template) Copy() error {
	if err := tmpl.Unmarshal(); err != nil {
		return err
	}
	basePath, err := BasePath(tmpl.Locator)
	if err != nil {
		return err
	}
	for i, basedOn := range tmpl.Config.BasedOn {
		absPath, err := AbsPath(basedOn, basePath)
		if err != nil {
			return err
		}
		if i == 0 {
			// basedOn can either be a single string, or a list of strings
			tmpl.expr.WriteString(fmt.Sprintf("| ($a.basedOn | select(type == \"!!str\")) |= %q\n", absPath))
			tmpl.expr.WriteString(fmt.Sprintf("| ($a.basedOn | select(type == \"!!seq\") | .[0]) |= %q\n", absPath))
		} else {
			tmpl.expr.WriteString(fmt.Sprintf("| $a.basedOn[%d] = %q\n", i, absPath))
		}
	}
	for i, p := range tmpl.Config.Probes {
		if p.File != nil {
			absPath, err := AbsPath(*p.File, basePath)
			if err != nil {
				return err
			}
			tmpl.expr.WriteString(fmt.Sprintf("| $a.probes[%d].file = %q\n", i, absPath))
		}
	}
	for i, p := range tmpl.Config.Provision {
		if p.File != nil {
			absPath, err := AbsPath(*p.File, basePath)
			if err != nil {
				return err
			}
			tmpl.expr.WriteString(fmt.Sprintf("| $a.provision[%d].file = %q\n", i, absPath))
		}
	}
	err = tmpl.evaluateExpression()
	if err != nil {
		tmpl.Bytes = nil
	}
	return err
}
