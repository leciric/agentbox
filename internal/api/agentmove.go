package api

import (
	"context"
	"net/http"
)

// MoveAgentRequest is POST /v1/agents/{project}/{agent}/move: the cluster the
// agent's machine should run in next.
//
// "local" is this host, and is not what a move is for: an agent that runs here
// is an Incus instance, and moving one of those between clusters is not a pod
// to a pod. "auto" picks a cluster by the same rule a create does.
type MoveAgentRequest struct {
	Cluster string `json:"cluster"`
}

// MoveAgent starts a job that moves an agent's machine to another cluster: a
// checkpoint and push, its workspace to a claim there, and its old pod and claim
// gone once the new one answers.
func (c *Client) MoveAgent(ctx context.Context, ref string, req MoveAgentRequest) (Job, error) {
	var out Job
	return out, c.agentDo(ctx, http.MethodPost, ref, "/move", req, &out)
}
