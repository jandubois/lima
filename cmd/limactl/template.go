package main

import (
	"fmt"

	"github.com/lima-vm/lima/pkg/limatmpl"
	"github.com/spf13/cobra"
)

func newTemplateCommand() *cobra.Command {
	templateCommand := &cobra.Command{
		Use:           "template",
		Short:         "Lima template management",
		SilenceUsage:  true,
		SilenceErrors: true,
		GroupID:       advancedCommand,
		Hidden:        true,
	}
	templateCommand.AddCommand(
		newTemplatePrintCommand(),
		newTemplateValidateCommand(),
	)
	return templateCommand
}

func newTemplatePrintCommand() *cobra.Command {
	templatePrintCommand := &cobra.Command{
		Use:   "print FILE.yaml|URL",
		Short: "Print template",
		Args:  WrapArgsError(cobra.ExactArgs(1)),
		RunE:  templatePrintAction,
	}
	return templatePrintCommand
}

func templatePrintAction(cmd *cobra.Command, args []string) error {
	tmpl, err := limatmpl.Read(cmd.Context(), "", args[0])
	if err != nil {
		return err
	}
	if len(tmpl.Bytes) == 0 {
		return fmt.Errorf("don't know how to interpret %q as a template locator", args[0])
	}
	_, err = fmt.Fprint(cmd.OutOrStdout(), string(tmpl.Bytes))
	return err
}

func newTemplateValidateCommand() *cobra.Command {
	templateValidateCommand := &cobra.Command{
		Use:   "validate FILE.yaml|URL",
		Short: "Validate template",
		Args:  WrapArgsError(cobra.ExactArgs(1)),
		RunE:  validateAction,
	}
	templateValidateCommand.Flags().Bool("fill", false, "fill defaults")
	return templateValidateCommand
}
