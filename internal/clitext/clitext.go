// Package clitext puts cobra's own texts in Portuguese: the help and usage
// layout, the help/completion commands, the -h flag and the errors cobra
// produces for bad input. Both binaries call Localize on their root.
package clitext

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

const usageTemplate = `Uso:{{if .Runnable}}
  {{.UseLine}}{{if .HasAvailableFlags}} [opções]{{end}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} <comando>{{end}}{{if gt (len .Aliases) 0}}

Também responde por:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Exemplos:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}{{$cmds := .Commands}}{{if eq (len .Groups) 0}}

Comandos:{{range $cmds}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{else}}{{range $group := .Groups}}

{{.Title}}{{range $cmds}}{{if (and (eq .GroupID $group.ID) (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if not .AllChildCommandsHaveGroup}}

Outros comandos:{{range $cmds}}{{if (and (eq .GroupID "") (or .IsAvailableCommand (eq .Name "help")))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Opções:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Opções de todos os comandos:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} <comando> --help" para ver a ajuda de um comando.{{end}}
`

// Localize must run after every command was added to root.
func Localize(root *cobra.Command) {
	// Subcommands are listed in the order they were added (the order of
	// use), not alphabetically.
	cobra.EnableCommandSorting = false
	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate(`{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`)
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return Translate(err) })

	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	for _, c := range root.Commands() {
		switch c.Name() {
		case "help":
			c.Use = "help [comando]"
			c.Short = "Mostra a ajuda de um comando"
			c.Long = "Mostra a ajuda de qualquer comando: " + root.Name() + " help <comando>."
		case "completion":
			c.Short = "Gera o autocompletar para o seu shell (bash, zsh, fish, powershell)"
			c.Long = "Gera o script de autocompletar (Tab) para o seu shell. Veja a ajuda do shell desejado:\n" +
				"  " + root.Name() + " completion bash --help"
			for _, sh := range c.Commands() {
				sh.Short = "Autocompletar para " + sh.Name()
				sh.Long = "Gera o script de autocompletar para " + sh.Name() + ". Para carregar na sessão atual:\n\n" +
					loadHint(root.Name(), sh.Name())
				if f := sh.Flags().Lookup("no-descriptions"); f != nil {
					f.Usage = "sem a descrição de cada opção"
				}
			}
		}
	}
	walk(root, func(c *cobra.Command) {
		// A command that only groups others (app, key...) would show its help
		// in silence for a typo; say the subcommand does not exist instead.
		if c != root && c.HasSubCommands() && c.Run == nil && c.RunE == nil {
			c.Args = cobra.ArbitraryArgs
			c.RunE = func(c *cobra.Command, args []string) error {
				if len(args) == 0 {
					return c.Help()
				}
				return unknown(c, args[0])
			}
		}
		// A runnable root (the menu) takes no arguments: a typo there gets
		// the same message. A root without Run gets cobra's own error.
		if c == root && c.HasSubCommands() && (c.Run != nil || c.RunE != nil) {
			c.Args = func(c *cobra.Command, args []string) error {
				if len(args) > 0 {
					return unknown(c, args[0])
				}
				return nil
			}
		}
		c.DisableFlagsInUseLine = true
		c.InitDefaultHelpFlag()
		if f := c.Flags().Lookup("help"); f != nil {
			f.Usage = "mostra esta ajuda"
		}
	})
}

// unknown is the error for a subcommand name that does not exist, with
// the closest names as a suggestion.
func unknown(c *cobra.Command, name string) error {
	msg := fmt.Sprintf("comando desconhecido %q — veja \"%s --help\"", name, c.CommandPath())
	if c.SuggestionsMinimumDistance <= 0 {
		c.SuggestionsMinimumDistance = 2
	}
	if s := c.SuggestionsFor(name); len(s) > 0 {
		msg += "\nVocê quis dizer: " + strings.Join(s, ", ") + "?"
	}
	return fmt.Errorf("%s", msg)
}

func loadHint(bin, shell string) string {
	switch shell {
	case "bash":
		return "  source <(" + bin + " completion bash)"
	case "zsh":
		return "  source <(" + bin + " completion zsh)"
	case "fish":
		return "  " + bin + " completion fish | source"
	default:
		return "  " + bin + " completion powershell | Out-String | Invoke-Expression"
	}
}

func walk(c *cobra.Command, f func(*cobra.Command)) {
	f(c)
	for _, s := range c.Commands() {
		walk(s, f)
	}
}

var translations = []struct {
	re  *regexp.Regexp
	fmt func(m []string) string
}{
	{regexp.MustCompile(`^unknown command "([^"]*)" for "([^"]*)"(?:\n\nDid you mean this\?\n((?:\t.+\n)+))?`), func(m []string) string {
		s := fmt.Sprintf("comando desconhecido %q — veja \"%s --help\"", m[1], m[2])
		if m[3] != "" {
			s += "\nVocê quis dizer: " + strings.Join(strings.Fields(m[3]), ", ") + "?"
		}
		return s
	}},
	{regexp.MustCompile(`^unknown flag: (\S+)`), func(m []string) string { return "opção desconhecida: " + m[1] }},
	{regexp.MustCompile(`^unknown shorthand flag: '(.)'`), func(m []string) string { return "opção desconhecida: -" + m[1] }},
	{regexp.MustCompile(`^flag needs an argument: (?:'(.)' in )?(\S+)`), func(m []string) string {
		return "a opção --" + strings.TrimLeft(m[2], "-") + " precisa de um valor"
	}},
	{regexp.MustCompile(`^invalid argument "([^"]*)" for "([^"]*)" flag`), func(m []string) string {
		return fmt.Sprintf("valor inválido %q para %s", m[1], m[2])
	}},
	{regexp.MustCompile(`^accepts at most (\d+) arg\(s\), received (\d+)`), func(m []string) string {
		return fmt.Sprintf("argumentos demais: aceita no máximo %s, recebeu %s", m[1], m[2])
	}},
	{regexp.MustCompile(`^accepts (\d+) arg\(s\), received (\d+)`), func(m []string) string {
		return fmt.Sprintf("espera %s argumento(s), recebeu %s", m[1], m[2])
	}},
	{regexp.MustCompile(`^requires at least (\d+) arg\(s\), only received (\d+)`), func(m []string) string {
		return fmt.Sprintf("faltam argumentos: precisa de pelo menos %s, recebeu %s", m[1], m[2])
	}},
	{regexp.MustCompile(`^unknown command "([^"]*)" for "([^"]*)"`), nil},
	{regexp.MustCompile(`^if any flags in the group \[([^\]]*)\] are set none of the others can be; \[([^\]]*)\] were all set`), func(m []string) string {
		return "estas opções não podem ser usadas juntas: --" + strings.ReplaceAll(m[2], " ", ", --")
	}},
	{regexp.MustCompile(`^if any flags in the group \[([^\]]*)\] are set they must all be set; missing \[([^\]]*)\]`), func(m []string) string {
		return "estas opções vão juntas: --" + strings.ReplaceAll(m[1], " ", ", --") + " (faltou --" + strings.ReplaceAll(m[2], " ", ", --") + ")"
	}},
	{regexp.MustCompile(`^required flag\(s\) (.+) not set`), func(m []string) string {
		return "opção obrigatória não informada: " + strings.ReplaceAll(m[1], `"`, "")
	}},
}

// Translate rewrites cobra's English errors; other errors pass unchanged.
func Translate(err error) error {
	if err == nil {
		return nil
	}
	for _, t := range translations {
		if t.fmt == nil {
			continue
		}
		if m := t.re.FindStringSubmatch(err.Error()); m != nil {
			return fmt.Errorf("%s", t.fmt(m))
		}
	}
	return err
}
