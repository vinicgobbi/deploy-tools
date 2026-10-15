package clitext

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func tree() (*cobra.Command, *bytes.Buffer) {
	var out bytes.Buffer
	root := &cobra.Command{Use: "tool", Short: "Ferramenta", SilenceErrors: true, SilenceUsage: true}
	root.AddGroup(&cobra.Group{ID: "apps", Title: "Apps:"})
	var name string
	create := &cobra.Command{Use: "create <app>", Short: "Cria um app", GroupID: "apps", Args: cobra.MaximumNArgs(1),
		RunE: func(*cobra.Command, []string) error { return nil }}
	create.Flags().StringVar(&name, "name", "", "nome")
	create.Flags().Bool("a", false, "a")
	create.Flags().Bool("b", false, "b")
	create.MarkFlagsMutuallyExclusive("a", "b")
	list := &cobra.Command{Use: "list", Short: "Lista", GroupID: "apps", Run: func(*cobra.Command, []string) {}}
	key := &cobra.Command{Use: "key", Short: "Chaves"}
	key.AddCommand(&cobra.Command{Use: "generate", Short: "Gera", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(create, list, key)
	root.SetOut(&out)
	root.SetErr(&out)
	Localize(root)
	return root, &out
}

func TestHelpInPortuguese(t *testing.T) {
	root, out := tree()
	root.SetArgs([]string{"--help"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"Uso:", "Apps:\n  create", "Outros comandos:", "help        Mostra a ajuda de um comando",
		"completion  Gera o autocompletar", "-h, --help   mostra esta ajuda", `Use "tool <comando> --help"`} {
		if !strings.Contains(got, want) {
			t.Errorf("faltou %q:\n%s", want, got)
		}
	}
	// Order of use, not alphabetical: create before list was added first anyway;
	// "help"/"completion" are not in the Apps group.
	for _, bad := range []string{"Usage:", "Available Commands", "Flags:", "help for", "[flags]"} {
		if strings.Contains(got, bad) {
			t.Errorf("sobrou inglês %q:\n%s", bad, got)
		}
	}
	out.Reset()
	root.SetArgs([]string{"create", "--help"})
	_ = root.Execute()
	if !strings.Contains(out.String(), "tool create <app> [opções]") {
		t.Fatalf("uso:\n%s", out)
	}
}

func TestErrorsInPortuguese(t *testing.T) {
	for args, want := range map[string]string{
		"crate":          "comando desconhecido \"crate\" — veja \"tool --help\"\nVocê quis dizer: create?",
		"create --nope":  "opção desconhecida: --nope",
		"create -z":      "opção desconhecida: -z",
		"create --name":  "a opção --name precisa de um valor",
		"create x y":     "argumentos demais: aceita no máximo 1, recebeu 2",
		"create --a --b": "estas opções não podem ser usadas juntas: --a, --b",
		"key genrate":    "comando desconhecido \"genrate\" — veja \"tool key --help\"\nVocê quis dizer: generate?",
	} {
		root, _ := tree()
		root.SetArgs(strings.Fields(args))
		err := Translate(root.Execute())
		if err == nil || err.Error() != want {
			t.Errorf("%s: %v, want %q", args, err, want)
		}
	}
}

func TestRunnableRootSuggests(t *testing.T) {
	root, _ := tree()
	root.RunE = func(*cobra.Command, []string) error { return nil }
	Localize(root)
	root.SetArgs([]string{"lsit"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "Você quis dizer: list?") {
		t.Fatalf("err = %v", err)
	}
}
