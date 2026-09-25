package runtimeconfig

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const maxRiskyTTL = 15 * time.Minute

var (
	ErrSeqReplay      = errors.New("runtimeconfig: seq out of order or replay")
	ErrReasonRequired = errors.New("runtimeconfig: reason required for risky event")
	ErrTTLRequired    = errors.New("runtimeconfig: TTL required for risky event")
	ErrTTLExceedsMax  = errors.New("runtimeconfig: TTL exceeds maximum allowed")
	ErrUnmaskedDenied = errors.New("runtimeconfig: static config prohibits unmasked mode")
	ErrAuditBlocked   = errors.New("runtimeconfig: audit write failed; risky event blocked")
)

type StaticConfig struct {
	AllowUnmasked bool
}

type TraceConfig struct {
	Enabled    bool
	SampleRate float64
	OnError    bool
	Policies   map[string]bool
}

type MaskConfig struct {
	Enabled bool
	Profile string
}

type RuntimeConfig struct {
	Trace         TraceConfig
	Mask          MaskConfig
	IncludeValues bool
}

type Scope struct {
	Global   bool
	PolicyID string
}

func GlobalScope() Scope { return Scope{Global: true} }

func PolicyScope(id string) Scope { return Scope{PolicyID: id} }

func (s Scope) String() string {
	if s.Global || s.PolicyID == "" {
		return "global"
	}
	return "policy:" + s.PolicyID
}

type EventType string

const (
	EventTraceEnable          EventType = "trace.enable"
	EventTraceDisable         EventType = "trace.disable"
	EventMaskEnable           EventType = "mask.enable"
	EventMaskDisable          EventType = "mask.disable"
	EventIncludeValuesEnable  EventType = "includeValues.enable"
	EventIncludeValuesDisable EventType = "includeValues.disable"
)

func (t EventType) isRisky() bool {
	return t == EventMaskDisable || t == EventIncludeValuesEnable
}

type Event struct {
	Type       EventType
	Scope      Scope
	TTLSeconds int
	Reason     string
	Seq        uint64
}

type Identity struct {
	ID     string
	Role   string
	Method string
}

type Authenticator interface {
	Authenticate(ctx context.Context, creds string) (Identity, error)
}

type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) (cancel func())
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) AfterFunc(d time.Duration, f func()) func() {
	t := time.AfterFunc(d, f)
	return func() { t.Stop() }
}

type Manager struct {
	ptr    atomic.Pointer[RuntimeConfig]
	static StaticConfig
	audit  AuditSink
	auth   Authenticator
	clock  Clock

	mu           sync.Mutex
	lastSeq      uint64
	cancelRevert func()
	instanceID   string
}

func NewManager(
	static StaticConfig,
	baseline RuntimeConfig,
	auth Authenticator,
	audit AuditSink,
	instanceID string,
) *Manager {
	m := &Manager{
		static:       static,
		audit:        audit,
		auth:         auth,
		clock:        realClock{},
		instanceID:   instanceID,
		cancelRevert: func() {},
	}
	m.ptr.Store(&baseline)
	return m
}

func newManagerWithClock(
	static StaticConfig,
	baseline RuntimeConfig,
	auth Authenticator,
	audit AuditSink,
	instanceID string,
	clk Clock,
) *Manager {
	m := NewManager(static, baseline, auth, audit, instanceID)
	m.clock = clk
	return m
}

func (m *Manager) Load() *RuntimeConfig {
	return m.ptr.Load()
}

func (m *Manager) Apply(ctx context.Context, ev Event, creds, sourceIP string) error {
	identity, err := m.auth.Authenticate(ctx, creds)
	if err != nil {
		_ = m.audit.Record(ctx, m.buildEntry(auditArgs{
			ev: ev, by: "unknown", sourceIP: sourceIP,
			result: "rejected", note: "auth: " + err.Error(),
		}))
		return fmt.Errorf("runtimeconfig: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if ev.Seq <= m.lastSeq {
		_ = m.audit.Record(ctx, m.buildEntry(auditArgs{
			ev: ev, identity: identity, sourceIP: sourceIP,
			result: "rejected", note: fmt.Sprintf("seq replay: got %d last %d", ev.Seq, m.lastSeq),
		}))
		return fmt.Errorf("%w: got %d, last %d", ErrSeqReplay, ev.Seq, m.lastSeq)
	}

	if ev.Type.isRisky() {
		if ev.Reason == "" {
			_ = m.audit.Record(ctx, m.buildEntry(auditArgs{
				ev: ev, identity: identity, sourceIP: sourceIP,
				result: "rejected", note: "no reason",
			}))
			return ErrReasonRequired
		}
		if ev.TTLSeconds <= 0 {
			_ = m.audit.Record(ctx, m.buildEntry(auditArgs{
				ev: ev, identity: identity, sourceIP: sourceIP,
				result: "rejected", note: "no TTL",
			}))
			return ErrTTLRequired
		}
		maxSec := int(maxRiskyTTL.Seconds())
		if ev.TTLSeconds > maxSec {
			_ = m.audit.Record(ctx, m.buildEntry(auditArgs{
				ev: ev, identity: identity, sourceIP: sourceIP,
				result: "rejected", note: fmt.Sprintf("TTL %d > max %d", ev.TTLSeconds, maxSec),
			}))
			return fmt.Errorf("%w: %ds > %ds", ErrTTLExceedsMax, ev.TTLSeconds, maxSec)
		}
	}

	if ev.Type == EventMaskDisable && !m.static.AllowUnmasked {
		_ = m.audit.Record(ctx, m.buildEntry(auditArgs{
			ev: ev, identity: identity, sourceIP: sourceIP,
			result: "rejected", note: "static ceiling: allowUnmasked=false",
		}))
		return ErrUnmaskedDenied
	}

	now := m.clock.Now()
	current := m.ptr.Load()
	next := applyEvent(current, ev)

	args := auditArgs{
		ev:          ev,
		identity:    identity,
		sourceIP:    sourceIP,
		result:      "applied",
		before:      current,
		after:       next,
		requestedAt: now,
	}
	if ev.Type.isRisky() {
		exp := now.Add(time.Duration(ev.TTLSeconds) * time.Second)
		args.expiresAt = &exp
	}

	if writeErr := m.audit.Record(ctx, m.buildEntry(args)); writeErr != nil {
		if ev.Type.isRisky() {
			return fmt.Errorf("%w: %v", ErrAuditBlocked, writeErr)
		}
	}

	m.ptr.Store(next)
	m.lastSeq = ev.Seq

	if ev.Type.isRisky() {
		m.cancelRevert()
		ttl := time.Duration(ev.TTLSeconds) * time.Second
		origType, origSeq, byID := ev.Type, ev.Seq, identity.ID
		m.cancelRevert = m.clock.AfterFunc(ttl, func() {
			m.revert(origType, origSeq, byID, sourceIP)
		})
	}

	return nil
}

func (m *Manager) revert(origType EventType, origSeq uint64, byID, sourceIP string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	current := m.ptr.Load()
	reverted := *current

	switch origType {
	case EventMaskDisable:
		if reverted.Mask.Enabled {
			return
		}
		reverted.Mask.Enabled = true
	case EventIncludeValuesEnable:
		if !reverted.IncludeValues {
			return
		}
		reverted.IncludeValues = false
	default:
		return
	}

	now := m.clock.Now()
	entry := AuditEntry{
		EventID:     newID(),
		Action:      "auto.revert",
		Scope:       "global",
		Before:      current,
		After:       &reverted,
		RequestedBy: "system",
		Reason:      fmt.Sprintf("TTL expired for seq=%d by %s", origSeq, byID),
		SourceIP:    sourceIP,
		RequestedAt: now,
		RevertedAt:  &now,
		InstanceID:  m.instanceID,
		Result:      "applied",
	}
	_ = m.audit.Record(context.Background(), entry)
	m.ptr.Store(&reverted)
}

func applyEvent(current *RuntimeConfig, ev Event) *RuntimeConfig {
	next := *current
	switch ev.Type {
	case EventTraceEnable:
		if ev.Scope.Global {
			next.Trace.Enabled = true
		} else {
			next.Trace.Policies = copyMap(current.Trace.Policies)
			next.Trace.Policies[ev.Scope.PolicyID] = true
		}
	case EventTraceDisable:
		if ev.Scope.Global {
			next.Trace.Enabled = false
		} else {
			next.Trace.Policies = copyMap(current.Trace.Policies)
			next.Trace.Policies[ev.Scope.PolicyID] = false
		}
	case EventMaskEnable:
		next.Mask.Enabled = true
	case EventMaskDisable:
		next.Mask.Enabled = false
	case EventIncludeValuesEnable:
		next.IncludeValues = true
	case EventIncludeValuesDisable:
		next.IncludeValues = false
	}
	return &next
}

type auditArgs struct {
	ev          Event
	identity    Identity
	by          string
	sourceIP    string
	result      string
	note        string
	before      *RuntimeConfig
	after       *RuntimeConfig
	requestedAt time.Time
	expiresAt   *time.Time
}

func (m *Manager) buildEntry(a auditArgs) AuditEntry {
	by := a.identity.ID
	if by == "" {
		by = a.by
	}
	now := a.requestedAt
	if now.IsZero() {
		now = m.clock.Now()
	}
	e := AuditEntry{
		EventID:     newID(),
		Seq:         a.ev.Seq,
		Action:      string(a.ev.Type),
		Scope:       a.ev.Scope.String(),
		Before:      a.before,
		After:       a.after,
		RequestedBy: by,
		Auth:        a.identity.Method,
		Reason:      a.ev.Reason,
		SourceIP:    a.sourceIP,
		RequestedAt: now,
		ExpiresAt:   a.expiresAt,
		InstanceID:  m.instanceID,
		Result:      a.result,
	}
	if a.result == "applied" {
		e.AppliedAt = &now
	}
	return e
}

func copyMap(src map[string]bool) map[string]bool {
	dst := make(map[string]bool, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
