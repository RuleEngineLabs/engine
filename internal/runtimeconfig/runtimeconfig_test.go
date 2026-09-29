package runtimeconfig

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type mockAuth struct {
	identity Identity
	err      error
}

func (a *mockAuth) Authenticate(_ context.Context, _ string) (Identity, error) {
	return a.identity, a.err
}

func okAuth() *mockAuth {
	return &mockAuth{identity: Identity{ID: "test-user", Role: "admin", Method: "token"}}
}

func failAuth(msg string) *mockAuth {
	return &mockAuth{err: errors.New(msg)}
}

type testSink struct {
	mu      sync.Mutex
	entries []AuditEntry
	failOn  func(AuditEntry) bool
}

func (s *testSink) Record(_ context.Context, entry AuditEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failOn != nil && s.failOn(entry) {
		return errors.New("simulated audit failure")
	}
	s.entries = append(s.entries, entry)
	return nil
}

func (s *testSink) last() AuditEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[len(s.entries)-1]
}

func (s *testSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

func (s *testSink) find(result string) (AuditEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.Result == result {
			return e, true
		}
	}
	return AuditEntry{}, false
}

type mockClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []mockTimer
}

type mockTimer struct {
	deadline  time.Time
	fn        func()
	cancelled bool
}

func newMockClock() *mockClock {
	return &mockClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *mockClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mockClock) AfterFunc(d time.Duration, f func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	idx := len(c.timers)
	c.timers = append(c.timers, mockTimer{deadline: c.now.Add(d), fn: f})
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.timers[idx].cancelled = true
	}
}

func (c *mockClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	var toFire []func()
	for i := range c.timers {
		if !c.timers[i].cancelled && !c.now.Before(c.timers[i].deadline) {
			toFire = append(toFire, c.timers[i].fn)
			c.timers[i].cancelled = true
		}
	}
	c.mu.Unlock()
	for _, fn := range toFire {
		fn()
	}
}

func defaultBaseline() RuntimeConfig {
	return RuntimeConfig{
		Trace: TraceConfig{Enabled: true, SampleRate: 1.0},
		Mask:  MaskConfig{Enabled: true},
	}
}

func newTestManager(sink *testSink, static StaticConfig, clk *mockClock) *Manager {
	return newManagerWithClock(static, defaultBaseline(), okAuth(), sink, "test-instance", clk)
}

func applyOK(t *testing.T, m *Manager, ev Event) {
	t.Helper()
	if err := m.Apply(context.Background(), ev, "creds", "127.0.0.1"); err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
}

func applyErr(t *testing.T, m *Manager, ev Event, wantErr error) {
	t.Helper()
	err := m.Apply(context.Background(), ev, "creds", "127.0.0.1")
	if err == nil {
		t.Fatal("Apply: expected error, got nil")
	}
	if wantErr != nil && !errors.Is(err, wantErr) {
		t.Fatalf("Apply: got %v, want errors.Is(%v)", err, wantErr)
	}
}

func riskyEvent(seq uint64) Event {
	return Event{
		Type:       EventMaskDisable,
		Scope:      GlobalScope(),
		Seq:        seq,
		Reason:     "debugging prod issue",
		TTLSeconds: 60,
	}
}

func TestApply_AuthFailure_RejectedAndAudited(t *testing.T) {
	sink := &testSink{}
	clk := newMockClock()
	m := newManagerWithClock(
		StaticConfig{AllowUnmasked: true},
		defaultBaseline(),
		failAuth("invalid token"),
		sink,
		"test-instance",
		clk,
	)

	err := m.Apply(context.Background(), riskyEvent(1), "bad-creds", "10.0.0.1")
	if err == nil {
		t.Fatal("expected error for auth failure")
	}
	if sink.count() != 1 {
		t.Fatalf("expected 1 audit entry, got %d", sink.count())
	}
	e := sink.last()
	if e.Result != "rejected" {
		t.Errorf("audit result: got %q, want %q", e.Result, "rejected")
	}
	if e.RequestedBy != "unknown" {
		t.Errorf("requestedBy: got %q, want %q", e.RequestedBy, "unknown")
	}
}

func TestApply_SeqReplay_RejectedAndAudited(t *testing.T) {
	cases := []struct {
		name      string
		firstSeq  uint64
		replaySeq uint64
	}{
		{"same seq", 5, 5},
		{"lower seq", 5, 3},
		{"zero seq", 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &testSink{}
			clk := newMockClock()
			m := newTestManager(sink, StaticConfig{AllowUnmasked: true}, clk)

			if c.firstSeq > 0 {
				applyOK(t, m, Event{Type: EventTraceDisable, Scope: GlobalScope(), Seq: c.firstSeq})
			}

			beforeCount := sink.count()
			applyErr(t, m, Event{Type: EventTraceDisable, Scope: GlobalScope(), Seq: c.replaySeq}, ErrSeqReplay)

			if sink.count() != beforeCount+1 {
				t.Errorf("expected audit entry for rejection, count before=%d after=%d", beforeCount, sink.count())
			}
			e := sink.last()
			if e.Result != "rejected" {
				t.Errorf("audit result: got %q, want %q", e.Result, "rejected")
			}
		})
	}
}

func TestApply_RiskyEvent_ValidationRejections(t *testing.T) {
	cases := []struct {
		name    string
		ev      Event
		wantErr error
	}{
		{
			name:    "mask.disable without reason",
			ev:      Event{Type: EventMaskDisable, Scope: GlobalScope(), Seq: 1, TTLSeconds: 60},
			wantErr: ErrReasonRequired,
		},
		{
			name:    "mask.disable without TTL",
			ev:      Event{Type: EventMaskDisable, Scope: GlobalScope(), Seq: 1, Reason: "test"},
			wantErr: ErrTTLRequired,
		},
		{
			name:    "mask.disable with negative TTL",
			ev:      Event{Type: EventMaskDisable, Scope: GlobalScope(), Seq: 1, Reason: "test", TTLSeconds: -1},
			wantErr: ErrTTLRequired,
		},
		{
			name:    "mask.disable TTL exceeds max (15 min)",
			ev:      Event{Type: EventMaskDisable, Scope: GlobalScope(), Seq: 1, Reason: "test", TTLSeconds: int(maxRiskyTTL.Seconds()) + 1},
			wantErr: ErrTTLExceedsMax,
		},
		{
			name:    "includeValues.enable without reason",
			ev:      Event{Type: EventIncludeValuesEnable, Scope: GlobalScope(), Seq: 1, TTLSeconds: 30},
			wantErr: ErrReasonRequired,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &testSink{}
			m := newTestManager(sink, StaticConfig{AllowUnmasked: true}, newMockClock())
			applyErr(t, m, c.ev, c.wantErr)
			if sink.count() == 0 {
				t.Error("expected audit entry on rejection, got none")
			}
			if e := sink.last(); e.Result != "rejected" {
				t.Errorf("audit result: got %q, want %q", e.Result, "rejected")
			}
		})
	}
}

func TestApply_MaskDisable_BlockedByStaticConfig(t *testing.T) {
	sink := &testSink{}
	m := newTestManager(sink, StaticConfig{AllowUnmasked: false}, newMockClock())

	applyErr(t, m, riskyEvent(1), ErrUnmaskedDenied)

	if sink.count() == 0 {
		t.Fatal("expected audit entry on rejection")
	}
	e := sink.last()
	if e.Result != "rejected" {
		t.Errorf("audit result: got %q, want %q", e.Result, "rejected")
	}
	if !m.Load().Mask.Enabled {
		t.Error("mask should still be enabled after rejected event")
	}
}

func TestApply_TTLExpiry_RevertsConfig(t *testing.T) {
	sink := &testSink{}
	clk := newMockClock()
	m := newTestManager(sink, StaticConfig{AllowUnmasked: true}, clk)

	applyOK(t, m, riskyEvent(1))

	if m.Load().Mask.Enabled {
		t.Fatal("mask should be disabled immediately after event")
	}

	clk.Advance(61 * time.Second)

	if !m.Load().Mask.Enabled {
		t.Error("mask should be re-enabled after TTL expiry")
	}

	found := false
	for _, e := range sink.entries {
		if e.Action == "auto.revert" {
			found = true
			if e.RevertedAt == nil {
				t.Error("auto.revert entry missing revertedAt")
			}
			if e.Result != "applied" {
				t.Errorf("auto.revert result: got %q, want %q", e.Result, "applied")
			}
		}
	}
	if !found {
		t.Error("expected auto.revert audit entry, none found")
	}
}

func TestApply_AuditFailure_RiskyBlockedSafeProceed(t *testing.T) {
	cases := []struct {
		name    string
		ev      Event
		wantErr bool
	}{
		{
			name:    "risky event blocked when audit fails",
			ev:      riskyEvent(1),
			wantErr: true,
		},
		{
			name:    "safe event proceeds despite audit failure",
			ev:      Event{Type: EventTraceDisable, Scope: GlobalScope(), Seq: 1},
			wantErr: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sink := &testSink{
				failOn: func(e AuditEntry) bool { return e.Result == "applied" },
			}
			m := newTestManager(sink, StaticConfig{AllowUnmasked: true}, newMockClock())

			err := m.Apply(context.Background(), c.ev, "creds", "127.0.0.1")

			if c.wantErr {
				if err == nil {
					t.Error("expected error for risky event with audit failure")
				}
				if !errors.Is(err, ErrAuditBlocked) {
					t.Errorf("expected ErrAuditBlocked, got: %v", err)
				}
				if !m.Load().Mask.Enabled {
					t.Error("mask should still be enabled after blocked risky event")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for safe event: %v", err)
				}
				if m.Load().Trace.Enabled {
					t.Error("trace should be disabled after safe event")
				}
			}
		})
	}
}

func TestLoad_InProgressExecutionKeepsOldSnapshot(t *testing.T) {
	sink := &testSink{}
	m := newTestManager(sink, StaticConfig{AllowUnmasked: true}, newMockClock())

	snapAtStart := m.Load()

	applyOK(t, m, riskyEvent(1))

	if m.Load().Mask.Enabled {
		t.Error("new load should see mask disabled")
	}
	if !snapAtStart.Mask.Enabled {
		t.Error("snapshot captured before the event must still have mask enabled")
	}
}

func TestNewManager_RestartReturnsToBaseline(t *testing.T) {
	static := StaticConfig{AllowUnmasked: true}
	base := defaultBaseline()
	sink := &testSink{}
	clk := newMockClock()

	m := newManagerWithClock(static, base, okAuth(), sink, "inst", clk)
	applyOK(t, m, riskyEvent(1))
	if m.Load().Mask.Enabled {
		t.Fatal("mask should be disabled after event")
	}

	m2 := newManagerWithClock(static, base, okAuth(), &testSink{}, "inst", newMockClock())
	if !m2.Load().Mask.Enabled {
		t.Error("restarted manager should start with baseline (mask enabled)")
	}
	if m2.Load().IncludeValues {
		t.Error("restarted manager should start with baseline (includeValues false)")
	}
}

func TestApply_SafeEvent_NoTTLRequired(t *testing.T) {
	sink := &testSink{}
	m := newTestManager(sink, StaticConfig{}, newMockClock())

	applyOK(t, m, Event{Type: EventTraceDisable, Scope: GlobalScope(), Seq: 1})
	if m.Load().Trace.Enabled {
		t.Error("trace should be disabled")
	}
	applyOK(t, m, Event{Type: EventMaskEnable, Scope: GlobalScope(), Seq: 2})
	if !m.Load().Mask.Enabled {
		t.Error("mask should be enabled")
	}
}

func TestApply_PerPolicyTraceOverride(t *testing.T) {
	sink := &testSink{}
	m := newTestManager(sink, StaticConfig{}, newMockClock())

	applyOK(t, m, Event{Type: EventTraceDisable, Scope: GlobalScope(), Seq: 1})
	applyOK(t, m, Event{Type: EventTraceEnable, Scope: PolicyScope("policy-A"), Seq: 2})

	cfg := m.Load()
	if cfg.Trace.Enabled {
		t.Error("global trace should remain disabled")
	}
	if !cfg.Trace.Policies["policy-A"] {
		t.Error("policy-A should have trace enabled")
	}
}

func TestApply_TTLCancelledByNewEvent(t *testing.T) {
	sink := &testSink{}
	clk := newMockClock()
	m := newTestManager(sink, StaticConfig{AllowUnmasked: true}, clk)

	applyOK(t, m, riskyEvent(1))
	applyOK(t, m, Event{
		Type: EventMaskDisable, Scope: GlobalScope(),
		Seq: 2, Reason: "still debugging", TTLSeconds: 120,
	})

	clk.Advance(61 * time.Second)
	if m.Load().Mask.Enabled {
		t.Error("mask should still be disabled — second TTL not yet expired")
	}

	clk.Advance(60 * time.Second)
	if !m.Load().Mask.Enabled {
		t.Error("mask should be re-enabled after second TTL expired")
	}
}

func TestAuditWriter_HashChain(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/audit.jsonl"
	w := NewAuditWriter(path)
	ctx := context.Background()

	e1 := AuditEntry{EventID: "1", Action: "trace.disable", Scope: "global",
		RequestedBy: "user", RequestedAt: time.Now(), InstanceID: "i1", Result: "applied"}
	if err := w.Record(ctx, e1); err != nil {
		t.Fatalf("Record 1: %v", err)
	}
	firstHash := w.prevHash

	e2 := AuditEntry{EventID: "2", Action: "mask.enable", Scope: "global",
		RequestedBy: "user", RequestedAt: time.Now(), InstanceID: "i1", Result: "applied"}
	if err := w.Record(ctx, e2); err != nil {
		t.Fatalf("Record 2: %v", err)
	}

	if w.prevHash == firstHash {
		t.Error("second entry should produce a different hash")
	}
	if firstHash == "" {
		t.Error("first hash should be non-empty")
	}
}
