package provisioning

// JobMessage is the queue payload for provisioning worker jobs.
type JobMessage struct {
	Kind  string `json:"kind"`
	JobID string `json:"job_id,omitempty"`
}
