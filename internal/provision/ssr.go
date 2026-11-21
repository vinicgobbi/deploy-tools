package provision

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/appconf"
	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/run"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// FirstSSRPort is where the port search for a new SSR app starts.
const FirstSSRPort = 4000

// NodePath finds the Node.js binary the SSR service runs, as an absolute
// path (systemd needs one). The web user runs it, so a Node installed
// under a home directory (nvm) will not do.
func (p *Provisioner) NodePath() (string, error) {
	out, err := run.Output(p.Runner, run.Cmd{Name: "node", Args: []string{"-e", "process.stdout.write(process.execPath)"}})
	if err != nil {
		return "", fmt.Errorf("Node.js não encontrado no servidor; o Angular SSR precisa dele (instale o Node da versão que o projeto usa, por exemplo pelo NodeSource): %w", err)
	}
	path := strings.TrimSpace(out)
	if path == "" {
		path = "/usr/bin/node"
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("caminho do node inesperado: %q", path)
	}
	if strings.HasPrefix(path, "/root/") || strings.HasPrefix(path, "/home/") {
		return "", fmt.Errorf("o node está em %s, uma pasta pessoal que o %s não acessa (nvm?); instale o Node no sistema, por exemplo pelo NodeSource",
			path, p.Paths.WebUser)
	}
	return path, nil
}

// usedPorts are the SSR ports in every app's .deploy.conf.
func (p *Provisioner) usedPorts() map[int]string {
	used := map[int]string{}
	entries, _ := os.ReadDir(p.Paths.AppsRoot)
	for _, e := range entries {
		if !e.IsDir() || validate.AppName(e.Name()) != nil {
			continue
		}
		if c, ok, err := appconf.Read(p.Paths.AppDir(e.Name())); err == nil && ok && c.SSR {
			used[c.Port] = e.Name()
		}
	}
	return used
}

// portFree reports whether nothing listens on the local port.
var portFree = func(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// NextSSRPort is the first port from FirstSSRPort that no app uses and
// nothing listens on.
func (p *Provisioner) NextSSRPort() (int, error) {
	used := p.usedPorts()
	for port := FirstSSRPort; port <= appconf.MaxPort; port++ {
		if _, taken := used[port]; !taken && portFree(port) {
			return port, nil
		}
	}
	return 0, errors.New("nenhuma porta livre para o servidor do SSR")
}

// CheckSSRPort validates a port chosen by hand for app.
func (p *Provisioner) CheckSSRPort(app string, port int) error {
	if port < appconf.MinPort || port > appconf.MaxPort {
		return fmt.Errorf("porta inválida: %d (use de %d a %d)", port, appconf.MinPort, appconf.MaxPort)
	}
	if other, taken := p.usedPorts()[port]; taken && other != app {
		return fmt.Errorf("a porta %d já é do app %s", port, other)
	}
	if !portFree(port) {
		return fmt.Errorf("a porta %d já está em uso neste servidor", port)
	}
	return nil
}

// SSRUnit is the systemd service running the Node server of app as the
// web user, from current/: a restart picks up the release just deployed.
// PORT is set on the command line, so a PORT in shared/.env (which the
// EnvironmentFile loads) cannot move the server away from the port the
// Apache proxy points at.
func (p *Provisioner) SSRUnit(app, node string, port int) string {
	current := p.Paths.CurrentLink(app)
	return fmt.Sprintf(`# Gerado por deploy-tools: servidor do Angular SSR de %[1]s.
# O deploy reinicia este serviço depois de trocar o current.
[Unit]
Description=Angular SSR de %[1]s (deploy-tools)
After=network.target
# Antes do primeiro deploy não há o que rodar.
ConditionPathExists=%[2]s/%[3]s

[Service]
Type=simple
User=%[4]s
Group=%[5]s
WorkingDirectory=%[2]s
Environment=NODE_ENV=production
EnvironmentFile=-%[6]s/.env
ExecStart=/usr/bin/env PORT=%[7]d %[8]s %[2]s/%[3]s
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=full
ProtectHome=yes

[Install]
WantedBy=multi-user.target
`, app, current, appconf.SSRServer, p.Paths.WebUser, p.Paths.WebGroup, p.Paths.SharedDir(app), port, node)
}

// SSRSudoers lets the deploy user (the CI forced command) restart only
// this app's service, and nothing else.
func (p *Provisioner) SSRSudoers(app string) string {
	return fmt.Sprintf(`# Gerado por deploy-tools: o deploy-ci reinicia o Angular SSR de %s.
%s ALL=(root) NOPASSWD: %s restart %s
`, app, p.Paths.DeployUser, p.Paths.Systemctl, config.ServiceName(app))
}

// SSRState is what is installed for an app's SSR service.
type SSRState struct {
	Unit, Sudoers bool   // the files exist
	Active        string // systemctl is-active ("active", "failed"...); "" when unknown
}

func (p *Provisioner) SSRState(app string) SSRState {
	st := SSRState{Unit: exists(p.Paths.UnitFile(app)), Sudoers: exists(p.Paths.SudoersFile(app))}
	if st.Unit {
		out, _ := run.Output(p.Runner, run.Cmd{Name: "systemctl", Args: []string{"is-active", config.ServiceName(app)}})
		st.Active = strings.TrimSpace(out)
	}
	return st
}

// syncSSR makes the service and the sudoers rule match the conf: written
// and enabled when SSR is on, stopped and removed otherwise. node is the
// Node binary (only needed when SSR is on).
func (p *Provisioner) syncSSR(app string, c appconf.Conf, node string) error {
	unit, sudoers, svc := p.Paths.UnitFile(app), p.Paths.SudoersFile(app), config.ServiceName(app)
	if !c.SSR {
		if exists(unit) {
			_, _ = run.Output(p.Runner, run.Cmd{Name: "systemctl", Args: []string{"disable", "--now", svc}})
			if err := os.Remove(unit); err != nil {
				return err
			}
			if _, err := run.Output(p.Runner, run.Cmd{Name: "systemctl", Args: []string{"daemon-reload"}}); err != nil {
				return err
			}
			p.say("Servidor do SSR desligado: removido %s", unit)
		}
		if err := os.Remove(sudoers); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}

	if err := p.writeSudoers(sudoers, p.SSRSudoers(app)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(unit), 0o755); err != nil {
		return err
	}
	if err := writeRootFile(unit, p.SSRUnit(app, node, c.Port), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", svc}} {
		if _, err := run.Output(p.Runner, run.Cmd{Name: "systemctl", Args: args}); err != nil {
			return err
		}
	}
	p.say("Servidor do SSR: %s (node na porta %d, como %s); o deploy o reinicia a cada release", unit, c.Port, p.Paths.WebUser)
	return nil
}

// writeSudoers installs a sudoers drop-in only after visudo accepts it: a
// broken file in sudoers.d breaks sudo for the whole machine. The temp
// file has a dot in its name, so sudo ignores it meanwhile.
func (p *Provisioner) writeSudoers(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o440); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o440); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if _, err := run.Output(p.Runner, run.Cmd{Name: "visudo", Args: []string{"-cf", tmp}}); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("regra do sudo recusada pelo visudo (o sudo está instalado?): %w", err)
	}
	return os.Rename(tmp, path)
}

// writeRootFile writes path atomically with mode, unless it already has
// exactly content.
func writeRootFile(path, content string, mode os.FileMode) error {
	if b, err := os.ReadFile(path); err == nil && string(b) == content {
		return nil
	}
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp")
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
