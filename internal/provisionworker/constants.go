package provisionworker

const (
	defaultControlPlaneStage = "lab"

	noteStartingDeployRunner          = "starting deploy runner"
	noteEnsuringInstanceConfiguration = "ensuring instance configuration"
	noteDeployRunnerInProgress        = "deploy runner in progress"
	noteStartingMcpWiringDeployRunner = "starting MCP wiring deploy runner" // #nosec G101 -- message string, not a credential
	noteProvisioned                   = "provisioned"
)
