package ai

type JobMessage struct {
	Kind   string   `json:"kind"`
	JobID  string   `json:"job_id,omitempty"`
	JobIDs []string `json:"job_ids,omitempty"`
}
