//go:build linux || darwin

// Package campaignservice joins frozen preparation to live ordinary dispatch and
// independent termination. It does not create Docker containers or resume journals.
package campaignservice

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeexec"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

var ErrService = errors.New("campaign service is not admitted or its live binding changed")

const TerminalTimeout = 30 * time.Second
const MaxCleanup = 64
const terminalReservation = "service-terminal"
const terminalCapacity = 70 << 20

type Peer interface {
	nativeexec.Peer
	ExecuteLifecycle(context.Context, interceptor.PreparedLifecycle) (interceptor.Response, error)
}
type Runtime interface {
	nativeexec.Runtime
	termination.Killer
}
type Config struct {
	HTTPS     *httpstarget.Client
	Prepared  *preparation.Stored
	Peer      Peer
	Runtime   Runtime
	Docker    campaign.DockerBinding
	StateRoot string
	Deadline  time.Time
	Model     *ModelConfig
	Evidence  *EvidenceConfig
	CheckHost func() error // Installed host lifetime gate; called before admission and every ordinary request.
}
type Service struct {
	httpsParent        *attemptadapter.Parent
	config             Config
	writer             *campaign.Writer
	attempts           *campaign.Attempts
	broker             *attemptadapter.Broker
	gate               chan struct{}
	admitted           atomic.Bool
	stopAccepted       atomic.Bool
	serving            atomic.Bool
	operations         map[string]bool
	live               atomic.Pointer[preparation.Target]
	restoring          atomic.Bool
	transitionEpoch    atomic.Uint64
	completion         completionState
	model              modelState
	artifacts          artifactStore
	snapshotAdmissions int64
	snapshotBytes      int64
	lineage            nativeLineage
	history            map[string]attemptadapter.Parent
	checkpointLineage  map[string]nativeLineage
	deadline           time.Time
	timer              *time.Timer
	killDone           chan struct{}
	kill               termination.Receipt
	shutdownOnce       sync.Once
	terminalDone       chan struct{}
	terminal           TerminalResult
	evidenceTargets    []evidenceTarget
	evidenceMu         sync.Mutex
}
type TerminalResult struct {
	Closure          string            `json:"closure"`
	CleanupConfirmed int               `json:"cleanup_confirmed"`
	CleanupRemaining int               `json:"cleanup_remaining"`
	CleanupState     string            `json:"cleanup_state"`
	TargetStop       string            `json:"target_stop"`
	Evidence         []EvidenceOutcome `json:"evidence"`
}

func New(ctx context.Context, c Config) (*Service, error) {
	if c.Prepared == nil || (c.Peer == nil && c.HTTPS == nil) || c.Runtime == nil || c.StateRoot == "" || ctx.Err() != nil {
		return nil, ErrService
	}
	if c.Evidence == nil {
		c.Evidence = &EvidenceConfig{MaxArchiveBytes: 4 << 30, Timeout: 2 * time.Minute, TotalTimeout: 5 * time.Minute}
	} else {
		copy := *c.Evidence
		c.Evidence = &copy
	}
	if c.Evidence.MaxArchiveBytes < 1 || c.Evidence.MaxArchiveBytes > contracts.MaxSafeInteger || c.Evidence.Timeout <= 0 || c.Evidence.Timeout > 5*time.Minute || c.Evidence.TotalTimeout <= 0 || c.Evidence.TotalTimeout > 30*time.Minute {
		return nil, ErrService
	}
	if m := c.Prepared.Target().Profile().HTTPS(); m != nil {
		if c.HTTPS == nil || c.HTTPS.MappingDigest() != m.Digest() {
			return nil, ErrService
		}
	} else if c.HTTPS != nil {
		return nil, ErrService
	}
	w := c.Prepared.Writer()
	m := w.Manifest()
	parsed, err := c.Prepared.Target().Protocol().ValidateEngineContext(c.Prepared.Context())
	if err != nil {
		return nil, err
	}
	operations := map[string]bool{}
	for _, name := range parsed["operations"].([]any) {
		if !slices.Contains([]string{"engine.attempt_execute", "engine.injection_delete", "engine.observation_read", "engine.snapshot_request", "engine.snapshot_list", "engine.snapshot_inspect", "engine.restore_request", "engine.artifact_begin", "engine.artifact_put_part", "engine.artifact_commit", "engine.model_generate", "engine.record_append", "engine.request_stop"}, name.(string)) {
			return nil, attemptadapter.ErrOperation
		}
		operations[name.(string)] = true
		if isSnapshot(name.(string)) {
			if _, ok := c.Peer.(SnapshotPeer); !ok {
				return nil, ErrService
			}
		}
	}
	if operations["engine.model_generate"] {
		if c.Model == nil || c.Model.Provider == nil || c.Model.ProfileDigest != m.ModelProfileDigest || c.Model.MaximumPromptTokens < 1 || c.Model.MaximumPromptTokens > 1<<40 {
			return nil, ErrService
		}
		copyModel := *c.Model
		copyModel.Tools = slices.Clone(c.Model.Tools)
		c.Model = &copyModel
	}
	persisted, err := campaign.ReadDockerBinding(c.StateRoot, m.CampaignID)
	if err != nil || persisted.RunManifestDigest != w.ManifestDigest() {
		return nil, ErrService
	}
	frozen, _ := json.Marshal(c.Docker)
	var binding campaign.DockerBinding
	_ = json.Unmarshal(frozen, &binding)
	c.Docker = binding
	highValue, _ := parsed["attempt_index_high_watermark"].(json.Number).Float64()
	high := int64(highValue)
	milliseconds, _ := parsed["remaining_limits"].(map[string]any)["campaign_time_ms"].(json.Number).Float64()
	if milliseconds < 1 || milliseconds > float64((30*time.Minute)/time.Millisecond) {
		return nil, ErrService
	}
	deadline := time.Now().Add(time.Duration(milliseconds) * time.Millisecond)
	if !c.Deadline.IsZero() && c.Deadline.Before(deadline) {
		deadline = c.Deadline
	}
	if !deadline.After(time.Now()) {
		return nil, ErrService
	}
	a, err := campaign.NewAttempts(w, high)
	if err != nil {
		return nil, err
	}
	if err = a.NativeSteps().VerifyRuntimeBinding(c.Docker); err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]any{"maximum_cleanup_actions": MaxCleanup, "timeout_ms": TerminalTimeout.Milliseconds()})
	if _, err = w.AppendReserving(campaign.Entry{RunRevision: m.InitialRevision, Kind: "service.terminal-reserved", Metadata: meta}, terminalReservation, terminalCapacity); err != nil {
		return nil, err
	}
	s := &Service{config: c, writer: w, attempts: a, gate: make(chan struct{}, 1), operations: operations, deadline: deadline, killDone: make(chan struct{}), terminalDone: make(chan struct{})}
	s.live.Store(c.Prepared.Target())
	s.artifacts = newArtifactStore()
	s.lineage = cloneLineage(nativeLineage{}, "")
	s.history = map[string]attemptadapter.Parent{}
	s.checkpointLineage = map[string]nativeLineage{}
	s.gate <- struct{}{}
	b, err := attemptadapter.NewBroker(attemptadapter.BrokerConfig{Catalog: c.Prepared.Target().Protocol().Catalog(), Protocol: c.Prepared.Target().Protocol(), Writer: w, Attempts: a, Deadline: deadline, Admitted: s.admitted.Load, Prepare: s.prepare, Cleanup: s.cleanupTarget, ReadKinds: func() []string { return s.target().ReadKinds() }})
	if err != nil {
		w.Fence().Stop(err)
		return nil, err
	}
	s.broker = b
	policy := campaign.EvidencePolicy{MaxArchiveBytes: c.Evidence.MaxArchiveBytes, TimeoutNS: int64(c.Evidence.Timeout), TotalTimeoutNS: int64(c.Evidence.TotalTimeout)}
	if _, err = w.Append(campaign.Entry{RunRevision: m.InitialRevision, Kind: "evidence.policy", Metadata: marshal(policy)}); err != nil {
		w.Fence().Stop(err)
		return nil, err
	}
	if c.HTTPS == nil {
		if err = s.rememberEvidenceTarget(c.Prepared.Target(), nil); err != nil {
			w.Fence().Stop(err)
			return nil, err
		}
	}
	// Arm before admission. Neither goroutine takes the ordinary gate or journal lock.
	go func() {
		s.kill = termination.New(c.Runtime).Watch(ctx, w.Fence(), c.StateRoot, c.Docker)
		close(s.killDone)
	}()
	s.timer = time.AfterFunc(time.Until(deadline), func() { s.Stop(context.DeadlineExceeded) })
	go func() {
		<-w.Fence().Done()
		s.admitted.Store(false)
		s.timer.Stop()
		s.shutdownOnce.Do(func() { go s.finish() })
	}()
	if c.HTTPS == nil {
		go s.watchTarget()
	}
	return s, nil
}

func (s *Service) target() *preparation.Target { return s.live.Load() }

func (s *Service) watchTarget() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.writer.Fence().Done():
			return
		case <-ticker.C:
			target := s.target()
			epoch := s.transitionEpoch.Load()
			if s.restoring.Load() {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), interceptor.QueryTimeout)
			status, err := s.config.Peer.Status(ctx, s.target().Live().CampaignID())
			cancel()
			if s.restoring.Load() || epoch != s.transitionEpoch.Load() || target != s.target() {
				continue
			}
			if err != nil || !status.Ready() || !status.Matches(s.target().InstanceID(), s.target().Live().Binding()) {
				s.Stop(ErrService)
				return
			}
		}
	}
}
func (s *Service) Stop(err error) { s.admitted.Store(false); s.writer.Fence().Stop(err) }
func (s *Service) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.gate:
		return nil
	}
}
func (s *Service) release() { s.gate <- struct{}{} }
func (s *Service) guard() (*nativeexec.Guard, error) {
	return nativeexec.NewGuard(s.config.Peer, s.config.Runtime, s.config.Docker, s.target().InstanceID(), s.target().Live().Binding())
}
func (s *Service) log(kind string, request, response []byte, terminal bool, release bool) error {
	entry := campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: kind, Metadata: json.RawMessage(`{}`)}
	for _, part := range []struct {
		role string
		raw  []byte
	}{{"request", request}, {"response", response}} {
		if len(part.raw) > 0 {
			if len(part.raw) > campaign.MaxContentBytes {
				return campaign.ErrInvalid
			}
			entry.Content = append(entry.Content, campaign.Content{Role: part.role, MediaType: "application/json", Bytes: part.raw})
		}
	}
	reservation := ""
	if terminal {
		reservation = terminalReservation
	}
	_, err := s.writer.AppendStored(entry, reservation, release)
	if err != nil {
		s.Stop(err)
	}
	return err
}
func (s *Service) inspect(ctx context.Context, terminal bool) (interceptor.SessionStatus, error) {
	return s.inspectTarget(ctx, terminal, s.target())
}
func (s *Service) inspectTarget(ctx context.Context, terminal bool, t *preparation.Target) (interceptor.SessionStatus, error) {
	b := t.Live().Binding()
	campaignID := t.Live().CampaignID()
	status, err := s.config.Peer.Status(ctx, campaignID)
	if err != nil || status.CampaignID != campaignID || !status.Matches(t.InstanceID(), b) || (!terminal && !status.Ready()) {
		return interceptor.SessionStatus{}, ErrService
	}
	deadline, _ := ctx.Deadline()
	if deadline.IsZero() {
		deadline = time.Now().Add(interceptor.QueryTimeout)
	}
	id := "inspect-" + termination.NewRequestID()
	p, err := interceptor.PrepareOperation(interceptor.OperationRequest{RequestID: id, OperationID: id, Operation: "session.status", CampaignID: campaignID, SessionID: b.SessionID, WorkerInstanceID: b.WorkerInstanceID, RunRevision: b.RunRevision, Deadline: deadline}, []byte(`{}`))
	if err != nil {
		return interceptor.SessionStatus{}, err
	}
	r, err := s.config.Peer.Execute(ctx, p)
	if logErr := s.log("service.native-status", p.Bytes(), boundedResponse(r), terminal, false); logErr != nil {
		return interceptor.SessionStatus{}, logErr
	}
	if err != nil {
		return interceptor.SessionStatus{}, err
	}
	if len(boundedResponse(r)) == 0 {
		return interceptor.SessionStatus{}, ErrService
	}
	view, err := interceptor.DecodeSessionStatus(r, campaignID, b)
	var native struct {
		Environment string `json:"environment_digest"`
		Application string `json:"application_digest"`
	}
	_ = json.Unmarshal(t.Live().Export().NativeJSON(), &native)
	if err != nil || view.Session.CapabilityManifestDigest != t.Live().Export().SourceDigest() || view.Session.FeedbackProfile != t.Live().NativeProfile() || view.Session.EnvironmentDigest != native.Environment || view.Session.AppDigest != native.Application || view.Owner.AllowTargetStop != t.Profile().Settings().AllowTargetStop || (!terminal && !view.Available) {
		return interceptor.SessionStatus{}, ErrService
	}
	return view, nil
}
func boundedResponse(r interceptor.Response) []byte {
	raw := r.Bytes()
	if len(raw) > 256<<10 {
		return nil
	}
	return raw
}
func (s *Service) prepare(ctx context.Context, raw []byte, deadline time.Time) (*attemptadapter.Plan, *nativeexec.Guard, error) {
	var view interceptor.SessionStatus
	var err error
	if s.config.HTTPS == nil {
		view, err = s.inspect(ctx, false)
	} else {
		err = s.config.Runtime.CheckRunning(ctx, s.config.Docker)
		view.Session.Revision = 1
	}
	if err != nil {
		s.Stop(err)
		return nil, nil, err
	}
	in, err := s.config.Prepared.InputsFor(s.target())
	if err != nil {
		s.Stop(err)
		return nil, nil, err
	}
	if err := s.addArtifacts(raw, &in); err != nil {
		s.Stop(err)
		return nil, nil, err
	}
	var request struct {
		Parent string `json:"parent_attempt_id"`
	}
	_ = json.Unmarshal(raw, &request)
	if parent, ok := s.lineage.Parents[request.Parent]; ok {
		in.Parent = &parent
	} else if parent, ok := s.history[request.Parent]; ok {
		in.PriorParent = &parent
	}
	in.KnownTurnIDs = append([]string{}, s.lineage.Turns...)
	in.SessionRevision = view.Session.Revision
	in.CreatedAt = time.Now()
	in.Deadline = deadline
	bound := in.CreatedAt.Add(time.Duration(s.target().Profile().Settings().OperationTimeoutMS) * time.Millisecond)
	if bound.Before(in.Deadline) {
		in.Deadline = bound
	}
	if s.config.HTTPS != nil {
		p, e := attemptadapter.CompileHTTPS(s.target().Protocol().Catalog(), raw, in, s.target().Profile().HTTPS(), s.config.HTTPS)
		if e != nil {
			return nil, nil, e
		}
		parent := p.HTTPSParent()
		s.httpsParent = &parent
		return p, nil, nil
	}
	p, err := attemptadapter.Compile(s.target().Protocol().Catalog(), raw, in)
	if err != nil {
		return nil, nil, err
	}
	g, err := s.guard()
	return p, g, err
}
func (s *Service) cleanupTarget(ctx context.Context, _ time.Time) (attemptadapter.CleanupTarget, error) {
	if !slices.Contains(s.target().Live().Export().ExecutionFacts().NativeOperations, "injection.delete") {
		return attemptadapter.CleanupTarget{}, attemptadapter.ErrPolicy
	}
	view, err := s.inspect(ctx, false)
	if err != nil {
		s.Stop(err)
		return attemptadapter.CleanupTarget{}, err
	}
	g, err := s.guard()
	return attemptadapter.CleanupTarget{Guard: g, Binding: s.target().Live().Binding(), SessionRevision: view.Session.Revision}, err
}

// Admit requires the complete validated startup transcript and pinned launch
// content. The launcher must verify the actual staged tree before calling it.
func (s *Service) Admit(ctx context.Context, in campaign.LaunchInputs) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	if s.admitted.Load() || s.writer.Fence().Err() != nil {
		return ErrService
	}
	if s.config.CheckHost != nil {
		if err := s.config.CheckHost(); err != nil {
			s.Stop(err)
			return err
		}
	}
	if err := s.writer.Manifest().ValidateLaunchInputs(s.target().Protocol(), in); err != nil {
		s.Stop(err)
		return err
	}
	s.initializeCompletion(in)
	if s.operations["engine.model_generate"] {
		policy, err := s.target().Protocol().ModelPolicyFromContext(s.config.Prepared.Context(), s.config.Model.Tools, in.Prompt)
		if err != nil {
			s.Stop(err)
			return err
		}
		s.model.policy = policy
	}
	if s.config.HTTPS == nil {
		if _, err := s.inspect(ctx, false); err != nil {
			s.Stop(err)
			return err
		}
	}
	if err := s.config.Runtime.CheckRunning(ctx, s.config.Docker); err != nil {
		s.Stop(err)
		return err
	}
	if err := s.log("service.admitted", nil, nil, false, false); err != nil {
		return err
	}
	if s.writer.Fence().Err() != nil {
		return ErrService
	}
	s.admitted.Store(true)
	return nil
}

// Handle retains the exact transport envelopes around broker work. Caller passes
// the transport's original absolute operation deadline, never a refreshed timer.
func (s *Service) Handle(ctx context.Context, raw []byte, sequence int64) ([]byte, error) {
	ctx, cancel := context.WithDeadline(ctx, s.deadline)
	defer cancel()

	go func(watchCtx context.Context) {
		select {
		case <-watchCtx.Done():
		case <-s.writer.Fence().Done():
			cancel()
		}
	}(ctx)
	if err := s.acquire(ctx); err != nil {
		return nil, err
	}
	defer s.release()
	if !s.admitted.Load() || s.writer.Fence().Err() != nil {
		return nil, ErrService
	}
	if s.config.CheckHost != nil {
		if err := s.config.CheckHost(); err != nil {
			s.Stop(err)
			return nil, err
		}
	}
	request, err := s.target().Protocol().ValidateRequest(raw)
	if err != nil {
		s.Stop(err)
		return nil, err
	}
	if !s.operations[request["operation"].(string)] {
		s.Stop(attemptadapter.ErrOperation)
		return nil, attemptadapter.ErrOperation
	}
	ceiling := s.target().Profile().Settings().OperationTimeoutMS
	if request["operation"] == "engine.snapshot_request" || request["operation"] == "engine.restore_request" {
		ceiling = 300000
	}
	if request["operation"] == "engine.model_generate" {
		ceiling = 120000
	}
	milliseconds, _ := request["timeout_ms"].(json.Number).Float64()
	ctx, operationCancel := context.WithTimeout(ctx, time.Duration(min(int64(milliseconds), ceiling))*time.Millisecond)
	defer operationCancel()
	reservation := "wire-" + termination.NewRequestID()
	deadline, _ := ctx.Deadline()
	meta, _ := json.Marshal(map[string]any{"deadline": deadline.UTC().Format(time.RFC3339Nano)})
	entry := campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "service.request", Metadata: meta, Content: []campaign.Content{{Role: "request", MediaType: "application/json", Bytes: raw}}}
	if _, err := s.writer.AppendReserving(entry, reservation, campaign.MaxContentBytes+2*(campaign.MaxEventBytes+1)); err != nil {
		s.Stop(err)
		return nil, err
	}
	body := request["body"].(map[string]any)
	// Conclusion traffic is the guest's existing finish signal; it must not
	// depend on an extra RPC to open the reserved finalization allowance.
	if request["operation"] == "engine.request_stop" || request["operation"] == "engine.artifact_begin" && body["purpose"] == "conclusion" || request["operation"] == "engine.record_append" && body["record_kind"] == "conclusion" {
		if err := s.beginFinalization(); err != nil {
			return nil, err
		}
	}
	var result []byte
	if !s.phaseAllows(request["operation"].(string), request["body"].(map[string]any)) {
		result, err = s.stateEnvelope(raw, sequence, stateDenied("STATE_CHANGED"))
	} else if request["operation"] == "engine.record_append" || request["operation"] == "engine.request_stop" {
		result, err = s.handleCompletion(ctx, raw, sequence)
	} else if isSnapshot(request["operation"].(string)) {
		result, err = s.handleSnapshot(ctx, raw, sequence)
	} else if request["operation"] == "engine.model_generate" {
		result, err = s.handleModel(ctx, raw, sequence)
	} else if isArtifact(request["operation"].(string)) {
		result, err = s.handleArtifact(ctx, raw, sequence)
	} else {
		result, err = s.broker.Handle(ctx, raw, sequence)
		if err == nil && request["operation"] == "engine.attempt_execute" {
			err = s.rememberAttempt(raw, result)
		}
	}
	entry.Kind = "service.response"
	entry.RunRevision = s.attempts.Status().RunRevision
	entry.Content = nil
	if err == nil {
		entry.Content = []campaign.Content{{Role: "response", MediaType: "application/json", Bytes: result}}
	} else {
		entry.Kind = "service.reply-unavailable"
	}
	if _, logErr := s.writer.AppendStored(entry, reservation, true); logErr != nil {
		s.Stop(logErr)
		return nil, logErr
	}
	if err != nil {
		if !errors.Is(err, attemptadapter.ErrOperation) {
			s.Stop(err)
		}
		return nil, err
	}
	return result, nil
}

// ServeOrdinary joins the real FIFO/spool session after startup admission. Its
// pump/control consumer run separately so urgent termination can bypass a call.
func (s *Service) ServeOrdinary(ctx context.Context, channel *transport.Session) error {
	if channel == nil || !s.serving.CompareAndSwap(false, true) {
		return ErrService
	}
	ticker := time.NewTicker(transport.SpoolPoll)
	defer ticker.Stop()
	sequence := int64(0)
	for {
		select {
		case <-ctx.Done():
			s.Stop(ctx.Err())
			return ctx.Err()
		case <-s.writer.Fence().Done():
			return s.writer.Fence().Err()
		case <-ticker.C:
			raw, ok := channel.Receive("ordinary-out")
			if !ok {
				continue
			}
			deadline, ok := channel.OperationDeadline()
			if !ok {
				s.Stop(ErrService)
				return ErrService
			}
			call, cancel := context.WithDeadline(ctx, deadline)
			result, err := s.Handle(call, raw, sequence)
			cancel()
			if err != nil {
				s.Stop(err)
				return err
			}
			for {
				err = channel.Enqueue("ordinary-in", result)
				if err == nil {
					break
				}
				if !errors.Is(err, transport.ErrQueueFull) {
					s.Stop(err)
					return err
				}
				select {
				case <-ctx.Done():
					s.Stop(ctx.Err())
					return ctx.Err()
				case <-s.writer.Fence().Done():
					return s.writer.Fence().Err()
				case <-ticker.C:
				}
			}
			sequence++
		}
	}
}
func (s *Service) Wait(ctx context.Context) (TerminalResult, termination.Receipt, error) {
	select {
	case <-ctx.Done():
		return TerminalResult{}, termination.Receipt{}, ctx.Err()
	case <-s.terminalDone:
	}
	select {
	case <-ctx.Done():
		return cloneTerminal(s.terminal), termination.Receipt{}, ctx.Err()
	case <-s.killDone:
		return cloneTerminal(s.terminal), s.kill, nil
	}
}

// StopAccepted reports the durably accepted harness conclusion/stop decision.
// It does not assert successful native cleanup, guest exit or assessment success.
func (s *Service) StopAccepted() bool { return s.stopAccepted.Load() }
