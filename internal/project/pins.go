// Package project is the developer-side half of deploy-tools: it inspects a
// project checkout, generates its .github/workflows/deploy.yml and checks
// existing workflows against the conventions the server side relies on.
// It never touches the server and is the only thing deploy-dev ships.
package project

// Pin is an action reference fixed to a full commit SHA, with the version
// it corresponds to kept as a comment (org convention: never a mutable tag).
type Pin struct {
	Repo    string
	SHA     string
	Version string
}

// Uses renders the value of a `uses:` line.
func (p Pin) Uses() string { return p.Repo + "@" + p.SHA + " # " + p.Version }

// Actions used by the generated workflows; bump them here (and release) to
// roll out updates.
var (
	Checkout    = Pin{"actions/checkout", "11d5960a326750d5838078e36cf38b85af677262", "v4.4.0"}
	SetupPython = Pin{"actions/setup-python", "a26af69be951a213d495a4c3e4e4022e16d87065", "v5.6.0"}
	SetupNode   = Pin{"actions/setup-node", "49933ea5288caeca8642d1e84afbd3f7d6820020", "v4.4.0"}
	PnpmSetup   = Pin{"pnpm/action-setup", "b906affcce14559ad1aafd4ab0e942779e9f58b1", "v4.3.0"}
	SetupPHP    = Pin{"shivammathur/setup-php", "f3e473d116dcccaddc5834248c87452386958240", "2.37.2"}
	Commitizen  = Pin{"commitizen-tools/commitizen-action", "338bbd841b75aaee6bf5340e1fa12f6ab58ff9ff", "0.27.1"}
	// Carry the package from the build job to the deploy job.
	UploadArtifact   = Pin{"actions/upload-artifact", "043fb46d1a93c77aae656e7c1c64a875d1fc6a0a", "v7.0.1"}
	DownloadArtifact = Pin{"actions/download-artifact", "3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c", "v8.0.1"}
)

// CommitizenPy is the commitizen release pip installs on Gitea/Forgejo,
// where the (Docker) commitizen-action may not run.
const CommitizenPy = "4.19.1"

// DeployToolsRepo hosts the composite deploy action (action.yml).
const DeployToolsRepo = "vinicgobbi/deploy-tools"
