package ai

type JobMessage struct {
	Kind  string `json:"kind"`
	JobID string `json:"job_id"`
}
