package provisionworker

const (
	defaultControlPlaneStage = "lab"

	noteEnsuringInstanceConfiguration = "ensuring instance configuration"
	noteDeployRunnerInProgress        = "deploy runner in progress"
	noteStartingMcpWiringDeployRunner = "starting MCP wiring deploy runner" // #nosec G101 -- message string, not a credential
	noteStartingSoulDeployRunner      = "starting soul deploy runner"
	noteProvisioned                   = "provisioned"
)
