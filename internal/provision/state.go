package provision

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vinicgobbi/deploy-tools/internal/config"
	"github.com/vinicgobbi/deploy-tools/internal/run"
	"github.com/vinicgobbi/deploy-tools/internal/validate"
)

// InstallState says whether the running binary is the installed one or a
// loose copy (e.g. a release just downloaded to /tmp).
type InstallState struct {
	Self             string // real path of the running binary
	Installed        bool   // BinaryPath exists
	Running          bool   // Self is BinaryPath
	InstalledVersion string // version of BinaryPath; "" when unknown
	Wrapper          bool   // the deploy-ci symlink points at BinaryPath
}

// InstallState inspects the installation; version is this binary's.
func (p *Provisioner) InstallState(self, version string) InstallState {
	st := InstallState{Self: cleanPath(self)}
	bin := p.Paths.BinaryPath()
	if _, err := os.Stat(bin); err != nil {
		return st
	}
	st.Installed = true
	st.Running = st.Self == cleanPath(bin)
	if t, err := filepath.EvalSymlinks(p.Paths.WrapperPath()); err == nil {
		st.Wrapper = cleanPath(t) == cleanPath(bin)
	}
	if st.Running {
		st.InstalledVersion = version
	} else if out, err := run.Output(p.Runner, run.Cmd{Name: bin, Args: []string{"version"}}); err == nil {
		// First line is "deploy-tools <version>".
		if f := strings.Fields(strings.SplitN(out, "\n", 2)[0]); len(f) == 2 && f[0] == "deploy-tools" {
			st.InstalledVersion = f[1]
		}
	}
	return st
}

func cleanPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		p = r
	}
	return filepath.Clean(p)
}

// Secrets is what a GitHub repository needs to deploy to this server.
type Secrets struct {
	Host     string   // DEPLOY_HOST
	User     string   // DEPLOY_USER
	HostKeys []string // DEPLOY_HOST_KEY: known_hosts lines for Host
	CIKeys   []Key    // authorized keys restricted to deploy-ci (nil when unreadable)
}

// Secrets builds the GitHub secrets for host ("" = DEPLOY_HOST from the
// config, or the machine's hostname). The private key is never on the
// server, so it is not part of the result.
func (p *Provisioner) Secrets(host string) Secrets {
	if host == "" {
		host = p.Paths.DeployHost
	}
	if host == "" {
		host, _ = os.Hostname()
	}
	s := Secrets{Host: host, User: p.Paths.DeployUser}
	files, _ := filepath.Glob(filepath.Join(p.Paths.SSHDir, "ssh_host_*_key.pub"))
	sort.Slice(files, func(i, j int) bool { return hostKeyRank(files[i]) < hostKeyRank(files[j]) })
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if fl := strings.Fields(string(b)); len(fl) >= 2 {
			s.HostKeys = append(s.HostKeys, host+" "+fl[0]+" "+fl[1])
		}
	}
	if keys, err := p.Keys(); err == nil {
		s.CIKeys = []Key{}
		for _, k := range keys {
			if k.Command == p.Paths.WrapperPath() {
				s.CIKeys = append(s.CIKeys, k)
			}
		}
	}
	return s
}

// ed25519 first, then ecdsa, then rsa: the order ssh prefers.
func hostKeyRank(f string) int {
	for i, t := range []string{"ed25519", "ecdsa", "rsa"} {
		if strings.Contains(filepath.Base(f), "_"+t+"_") {
			return i
		}
	}
	return 9
}

// HostCandidate is an address the CI might use to reach this server.
type HostCandidate struct{ Value, Label string }

// HostCandidates lists the machine's hostname and its global unicast IPs
// (IPv4 first), labeled by interface. Behind NAT the public IP is not on
// any interface, so callers must also offer typing one.
func HostCandidates() []HostCandidate {
	var out []HostCandidate
	if h, err := os.Hostname(); err == nil && h != "" {
		out = append(out, HostCandidate{h, "hostname"})
	}
	ifaces, _ := net.Interfaces()
	var v4, v6 []HostCandidate
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || !ipn.IP.IsGlobalUnicast() {
				continue
			}
			label := "IP de " + ifc.Name
			if ipn.IP.IsPrivate() {
				label += " (privado — só se a CI estiver na mesma rede)"
			}
			if ipn.IP.To4() != nil {
				v4 = append(v4, HostCandidate{ipn.IP.String(), label})
			} else {
				v6 = append(v6, HostCandidate{ipn.IP.String(), label})
			}
		}
	}
	return append(append(out, v4...), v6...)
}

// SetDeployHost saves how the CI reaches this server (DEPLOY_HOST).
func (p *Provisioner) SetDeployHost(host string) error {
	if err := ValidateHost(host); err != nil {
		return err
	}
	if err := config.SetDeployHost(p.Paths.ConfigFile, host); err != nil {
		return err
	}
	p.Paths.DeployHost = host
	p.say("DEPLOY_HOST=%s gravado em %s.", host, p.Paths.ConfigFile)
	return nil
}

// ValidateHost accepts a hostname or an IP address.
func ValidateHost(host string) error {
	if net.ParseIP(host) != nil {
		return nil
	}
	if host != "" && validate.Domain(host) == nil {
		return nil
	}
	if host != "" && !strings.ContainsAny(host, " ./:") && validate.AppName(host) == nil {
		return nil // single-label hostname, e.g. on a private network
	}
	return fmt.Errorf("endereço inválido: %q (use um hostname ou um IP)", host)
}
