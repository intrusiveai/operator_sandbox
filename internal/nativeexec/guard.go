// Package nativeexec connects admitted attempt steps to Interceptor with durable
// audit, live identity checks and terminal handling. It is host-only infrastructure.
package nativeexec

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

var ErrBinding = errors.New("native execution binding is not ready or no longer matches")
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type Peer interface {
	Execute(context.Context, interceptor.PreparedOperation) (interceptor.Response, error)
	Status(context.Context, string) (interceptor.Status, error)
}
type Runtime interface {
	CheckRunning(context.Context, campaign.DockerBinding) error
}

// Guard pins one native revision and one exact harness container. Build a new
// guard only after a verified restore and durable parent-ledger rebind.
type Guard struct {
	peer     Peer
	runtime  Runtime
	docker   campaign.DockerBinding
	instance string
	binding  interceptor.Binding
}

func NewGuard(peer Peer, runtime Runtime, docker campaign.DockerBinding, instance string, binding interceptor.Binding) (*Guard, error) {
	if peer == nil || runtime == nil || docker.Validate() != nil || !idPattern.MatchString(instance) || !idPattern.MatchString(binding.SessionID) || !idPattern.MatchString(binding.WorkerInstanceID) || binding.RunRevision == 0 || binding.RunRevision > contracts.MaxSafeInteger {
		return nil, ErrBinding
	}
	labels := map[string]string{}
	for k, v := range docker.Labels {
		labels[k] = v
	}
	docker.Labels = labels
	return &Guard{peer, runtime, docker, instance, binding}, nil
}
func (g *Guard) check(ctx context.Context, p interceptor.PreparedOperation, reporting bool) ([]byte, error) {
	q := p.Request()
	if q.CampaignID != g.docker.CampaignID || !reporting && (q.SessionID != g.binding.SessionID || q.RunRevision != g.binding.RunRevision) {
		return nil, ErrBinding
	}
	status, err := g.peer.Status(ctx, q.CampaignID)
	if err != nil || status.InstanceID != g.instance || status.CampaignID != q.CampaignID {
		return nil, ErrBinding
	}
	if reporting {
		binding, ok := status.Sessions[q.SessionID]
		if !ok || binding.SessionID != q.SessionID {
			return nil, ErrBinding
		}
	} else {
		if !status.Ready() || !status.Matches(g.instance, g.binding) {
			return nil, ErrBinding
		}
		if err = g.runtime.CheckRunning(ctx, g.docker); err != nil {
			return nil, ErrBinding
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	proof, _ := json.Marshal(map[string]any{"instance_id": g.instance, "campaign_id": q.CampaignID, "session_id": q.SessionID, "run_revision": q.RunRevision, "docker_container_id": g.docker.DockerContainerID, "docker_daemon_id": g.docker.DaemonID, "reporting_only": reporting})
	return proof, nil
}
