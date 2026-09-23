package contracts

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
)

var ErrLoopStopped = errors.New("contract exploration stopped")

func harnessDefaults() map[string]int64 {
	return map[string]int64{"max_model_turns": 300, "max_tool_calls": 2000, "max_tool_calls_per_response": 16,
		"max_invalid_tool_calls": 50, "max_consecutive_invalid_tool_calls": 5,
		"max_read_bytes": 268435456, "max_no_progress_turns": 10}
}

// ResolveHarnessLimits applies administrator defaults and explicit policy caps.
// Pass {} for omitted configuration. No defaults are applied to wire requests.
func (p *Protocol) ResolveHarnessLimits(overrides, caps []byte) (map[string]int64, error) {
	result := harnessDefaults()
	for i, raw := range [][]byte{overrides, caps} {
		value, err := Decode(raw, ControlLimit)
		if err != nil {
			return nil, err
		}
		part, ok := value.(map[string]any)
		if !ok {
			return nil, ErrProtocol
		}
		full := map[string]any{}
		for key, n := range harnessDefaults() {
			full[key] = json.Number(strconv.FormatInt(n, 10))
		}
		for key, n := range part {
			full[key] = n
		}
		schema, ok := p.catalog.schemas["urn:operator:schema:harness-loop-limits:v1alpha1"]
		if !ok {
			return nil, ErrCatalog
		}
		if err := schema.Validate(full); err != nil {
			return nil, ErrSchema
		}
		for key, n := range part {
			if i == 0 {
				result[key] = number(n)
			} else {
				result[key] = min(result[key], number(n))
			}
		}
	}
	return result, nil
}

type LoopSnapshot struct {
	Mode                        string `json:"mode"`
	Reason                      string `json:"reason"`
	Phase                       string `json:"phase"`
	ActiveTool                  string `json:"active_tool"`
	ModelTurns                  int64  `json:"model_turns"`
	ToolCalls                   int64  `json:"tool_calls"`
	InvalidToolCalls            int64  `json:"invalid_tool_calls"`
	ConsecutiveInvalidToolCalls int64  `json:"consecutive_invalid_tool_calls"`
	ReadBytes                   int64  `json:"read_bytes"`
	ReservedReadBytes           int64  `json:"reserved_read_bytes"`
	NoProgressTurns             int64  `json:"no_progress_turns"`
	QueuedCalls                 int64  `json:"queued_calls"`
	SkippedCalls                int64  `json:"skipped_calls"` // Current/last batch, not campaign total.
}
type readSource struct{ kind, identity string }
type byteRange struct{ start, end int64 }
type readReservation struct {
	source       readSource
	offset, size int64
}

// HarnessLoop is one serialized campaign's in-memory accounting. Trusted callers
// perform duplicate lookup before charging new model admissions/tool dispatches.
// It performs no provider/native I/O, durable admission, attestation or scheduling.
type HarnessLoop struct {
	limits                map[string]int64
	state                 LoopSnapshot
	progress, compaction  bool
	read                  *readReservation
	ranges                map[readSource][]byteRange
	experiments, payloads map[string]bool
	finalization          *FinalizationBudget
}

func NewHarnessLoop(limits map[string]int64) (*HarnessLoop, error) {
	defaults := harnessDefaults()
	if len(limits) != len(defaults) {
		return nil, ErrProtocol
	}
	copyLimits := map[string]int64{}
	for key := range defaults {
		n := limits[key]
		if n < 1 || n > MaxSafeInteger {
			return nil, ErrProtocol
		}
		copyLimits[key] = n
	}
	return &HarnessLoop{limits: copyLimits, state: LoopSnapshot{Mode: "exploring", Phase: "idle"},
		ranges: map[readSource][]byteRange{}, experiments: map[string]bool{}, payloads: map[string]bool{}}, nil
}
func (l *HarnessLoop) Snapshot() LoopSnapshot { return l.state }
func (l *HarnessLoop) exploring() error {
	if l.state.Mode != "exploring" {
		return ErrLoopStopped
	}
	return nil
}
func (l *HarnessLoop) stop(reason string) {
	if l.state.Mode == "exploring" {
		l.state.Mode, l.state.Reason = "finalizing", reason
		l.state.SkippedCalls += l.state.QueuedCalls
		l.state.QueuedCalls = 0
	}
}

// Stop applies a trusted external graceful decision; model claims are not input.
func (l *HarnessLoop) Stop(reason string) error {
	if reason != "budget-limit" && reason != "harness-error" && reason != "no-useful-next-experiment" {
		return ErrProtocol
	}
	l.stop(reason)
	return nil
}

// HardStop bypasses graceful work, retaining uncertain read reservations.
func (l *HarnessLoop) HardStop() {
	l.state.Mode, l.state.Reason = "closed", "hard-stop"
	l.state.SkippedCalls += l.state.QueuedCalls
	l.state.QueuedCalls = 0
	if l.finalization != nil {
		l.finalization.Close()
	}
}
func safeNonnegative(n int64) bool { return n >= 0 && n <= MaxSafeInteger }

// NarrowRemaining applies verified host budgets at a quiescent boundary. Larger
// later reports cannot widen the retained local allowance or reset usage.
func (l *HarnessLoop) NarrowRemaining(modelTurns, readBytes int64) error {
	if err := l.exploring(); err != nil {
		return err
	}
	s := &l.state
	if !safeNonnegative(modelTurns) || !safeNonnegative(readBytes) || s.ActiveTool != "" || l.read != nil || s.Phase == "model" {
		return ErrProtocol
	}
	l.limits["max_model_turns"] = min(l.limits["max_model_turns"], s.ModelTurns+modelTurns)
	l.limits["max_read_bytes"] = min(l.limits["max_read_bytes"], s.ReadBytes+readBytes)
	if l.limits["max_read_bytes"] == s.ReadBytes || (s.Phase == "idle" && l.limits["max_model_turns"] == s.ModelTurns) {
		l.stop("budget-limit")
	}
	return nil
}

func (l *HarnessLoop) BeginModel(compaction bool) error {
	if err := l.exploring(); err != nil {
		return err
	}
	s := &l.state
	if s.Phase != "idle" || l.read != nil {
		return ErrProtocol
	}
	if s.ModelTurns >= l.limits["max_model_turns"] {
		l.stop("budget-limit")
		return ErrLoopStopped
	}
	s.ModelTurns++
	s.Phase, s.SkippedCalls = "model", 0
	l.compaction, l.progress = compaction, false
	return nil
}
func (l *HarnessLoop) AcceptResponse(toolCount int64) error {
	if err := l.exploring(); err != nil {
		return err
	}
	s := &l.state
	if s.Phase != "model" || !safeNonnegative(toolCount) {
		return ErrProtocol
	}
	if l.compaction && toolCount != 0 {
		l.HardStop()
		return ErrProtocol
	}
	s.Phase, s.QueuedCalls = "batch", toolCount
	if toolCount > l.limits["max_tool_calls_per_response"] {
		l.stop("budget-limit")
		return ErrLoopStopped
	}
	return nil
}
func (l *HarnessLoop) StartTool(name string) error {
	if err := l.exploring(); err != nil {
		return err
	}
	s := &l.state
	if !attemptIDPattern.MatchString(name) || s.Phase != "batch" || s.QueuedCalls == 0 || s.ActiveTool != "" || l.read != nil {
		return ErrProtocol
	}
	if s.ToolCalls >= l.limits["max_tool_calls"] {
		l.stop("budget-limit")
		return ErrLoopStopped
	}
	s.ToolCalls++
	s.QueuedCalls--
	s.ActiveTool = name
	return nil
}
func (l *HarnessLoop) FinishTool(outcome string) error {
	s := &l.state
	if s.Mode == "closed" || s.ActiveTool == "" || l.read != nil {
		return ErrProtocol
	}
	if outcome != "success" && outcome != "invalid" && outcome != "preflight-rejected" && outcome != "failed" && outcome != "restored" {
		return ErrProtocol
	}
	name := strings.TrimPrefix(s.ActiveTool, "engine.")
	if outcome == "restored" && name != "restore_request" {
		return ErrProtocol
	}
	if outcome == "invalid" {
		s.InvalidToolCalls++
		s.ConsecutiveInvalidToolCalls++
	} else if (outcome == "success" || outcome == "restored") && name != "record_append" {
		s.ConsecutiveInvalidToolCalls = 0
	}
	s.ActiveTool = ""
	if outcome == "restored" {
		s.SkippedCalls += s.QueuedCalls
		s.QueuedCalls = 0
	}
	if s.InvalidToolCalls >= l.limits["max_invalid_tool_calls"] || s.ConsecutiveInvalidToolCalls >= l.limits["max_consecutive_invalid_tool_calls"] {
		l.stop("harness-error")
	} else if s.ToolCalls >= l.limits["max_tool_calls"] {
		l.stop("budget-limit")
	}
	return nil
}

// ReserveRead runs before delivery. Content identity is its verified immutable
// digest; observation identity hashes the verified source/entry identity tuple.
func (l *HarnessLoop) ReserveRead(sourceKind, sourceID string, offset, size int64) error {
	if err := l.exploring(); err != nil {
		return err
	}
	s := &l.state
	if l.read != nil || !safeNonnegative(offset) || size < 1 || size > MaxSafeInteger || offset > MaxSafeInteger-size {
		return ErrProtocol
	}
	if (sourceKind != "content" && sourceKind != "observation") || !attemptDigestPattern.MatchString(sourceID) {
		return ErrProtocol
	}
	if s.Phase != "idle" && !(s.Phase == "batch" && s.ActiveTool != "") {
		return ErrProtocol
	}
	if size > l.limits["max_read_bytes"]-s.ReadBytes {
		l.stop("budget-limit")
		return ErrLoopStopped
	}
	l.read = &readReservation{readSource{sourceKind, sourceID}, offset, size}
	s.ReservedReadBytes = size
	return nil
}
func (l *HarnessLoop) SettleRead(actual int64) (bool, error) {
	s := &l.state
	if s.Mode == "closed" || l.read == nil || !safeNonnegative(actual) || actual > l.read.size {
		return false, ErrProtocol
	}
	r := *l.read
	s.ReadBytes += actual
	s.ReservedReadBytes = 0
	l.read = nil
	novel := false
	if actual > 0 {
		end, covered := r.offset+actual, int64(0)
		ranges := l.ranges[r.source]
		for _, old := range ranges {
			covered += max(int64(0), min(end, old.end)-max(r.offset, old.start))
		}
		novel = covered < actual
		ranges = append(ranges, byteRange{r.offset, end})
		sort.Slice(ranges, func(i, j int) bool { return ranges[i].start < ranges[j].start })
		merged := make([]byteRange, 0, len(ranges))
		for _, current := range ranges {
			if len(merged) > 0 && current.start <= merged[len(merged)-1].end {
				merged[len(merged)-1].end = max(merged[len(merged)-1].end, current.end)
			} else {
				merged = append(merged, current)
			}
		}
		l.ranges[r.source] = merged
		if novel && s.Phase == "batch" && !l.compaction {
			l.progress = true
		}
	}
	if s.ReadBytes == l.limits["max_read_bytes"] {
		l.stop("budget-limit")
	}
	return novel, nil
}
func (l *HarnessLoop) ExperimentCompleted(receiptID string) (bool, error) {
	s := &l.state
	if s.Mode == "closed" || s.Phase != "batch" || strings.TrimPrefix(s.ActiveTool, "engine.") != "attempt_execute" || !attemptIDPattern.MatchString(receiptID) {
		return false, ErrProtocol
	}
	novel := !l.experiments[receiptID]
	l.experiments[receiptID] = true
	l.progress = l.progress || novel
	return novel, nil
}
func (l *HarnessLoop) PayloadCommitted(digest string) (bool, error) {
	s := &l.state
	if s.Mode == "closed" || s.Phase != "batch" || s.ActiveTool == "" || !attemptDigestPattern.MatchString(digest) {
		return false, ErrProtocol
	}
	novel := !l.payloads[digest]
	l.payloads[digest] = true
	l.progress = l.progress || novel
	return novel, nil
}
func (l *HarnessLoop) EndTurn() error {
	s := &l.state
	if s.Mode == "closed" || s.Phase != "batch" || s.ActiveTool != "" || s.QueuedCalls != 0 || l.read != nil {
		return ErrProtocol
	}
	if l.progress && !l.compaction {
		s.NoProgressTurns = 0
	} else {
		s.NoProgressTurns++
	}
	s.Phase = "idle"
	if s.NoProgressTurns >= l.limits["max_no_progress_turns"] {
		l.stop("no-useful-next-experiment")
	} else if s.ModelTurns >= l.limits["max_model_turns"] {
		l.stop("budget-limit")
	}
	return nil
}

type FinalizationSnapshot struct {
	Requests        int64 `json:"requests"`
	ConclusionBytes int64 `json:"conclusion_bytes"`
	DeadlineMS      int64 `json:"deadline_ms"`
	Closed          bool  `json:"closed"`
}

// FinalizationBudget accounts trusted conclusion-only requests; it does not
// validate payload/receipt authority, schedule timers or terminate Docker.
type FinalizationBudget struct {
	state              FinalizationSnapshot
	lastMS, bytesLimit int64
}

// BeginFinalization starts once, after exploration and in-flight work have ended.
// Times use the same monotonic millisecond clock; callers enforce expiry actively.
func (l *HarnessLoop) BeginFinalization(nowMS, campaignDeadlineMS, artifactRemaining int64) (*FinalizationBudget, error) {
	s := &l.state
	if s.Mode != "finalizing" || s.Phase != "idle" || l.read != nil || l.finalization != nil || !safeNonnegative(nowMS) || !safeNonnegative(campaignDeadlineMS) || !safeNonnegative(artifactRemaining) {
		return nil, ErrProtocol
	}
	deadline := min(nowMS+30000, campaignDeadlineMS)
	b := &FinalizationBudget{state: FinalizationSnapshot{DeadlineMS: deadline, Closed: nowMS >= deadline}, lastMS: nowMS, bytesLimit: min(2<<20, artifactRemaining)}
	l.finalization = b
	return b, nil
}
func (b *FinalizationBudget) Snapshot() FinalizationSnapshot { return b.state }
func (b *FinalizationBudget) Close()                         { b.state.Closed = true }
func (b *FinalizationBudget) CheckTime(nowMS int64) error {
	if b.state.Closed {
		return ErrLoopStopped
	}
	if !safeNonnegative(nowMS) || nowMS < b.lastMS {
		b.Close()
		return ErrProtocol
	}
	b.lastMS = nowMS
	if nowMS >= b.state.DeadlineMS {
		b.Close()
		return ErrLoopStopped
	}
	return nil
}

// Charge counts each new ordinary request and distinct conclusion content bytes
// once before transfer, including an existing upload continued in finalization.
// Kind is a trusted classification, never a field accepted from the harness.
func (b *FinalizationBudget) Charge(kind string, conclusionBytes, nowMS int64) error {
	if err := b.CheckTime(nowMS); err != nil {
		return err
	}
	if (kind != "conclusion-artifact" && kind != "conclusion-record" && kind != "stop") || !safeNonnegative(conclusionBytes) || (kind != "conclusion-artifact" && conclusionBytes != 0) {
		return ErrProtocol
	}
	if b.state.Requests == 16 || conclusionBytes > b.bytesLimit-b.state.ConclusionBytes {
		b.Close()
		return ErrLoopStopped
	}
	b.state.Requests++
	b.state.ConclusionBytes += conclusionBytes
	return nil
}
