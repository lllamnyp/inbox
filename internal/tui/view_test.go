package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/lllamnyp/inbox/internal/derive"
	"github.com/lllamnyp/inbox/internal/state"
)

func TestStatusFadesButStaysInLog(t *testing.T) {
	st := &state.State{PRs: map[string]*derive.PR{}}
	m := New(st, "/dev/null", nil, func(string) bool { return false }, time.Minute)
	m.width, m.height = 100, 30

	m.setStatus("acked: org/repo#1")
	m.now = time.Now()
	if !strings.Contains(m.View(), "acked: org/repo#1") {
		t.Error("status should be visible right after being set")
	}

	m.now = time.Now().Add(statusTTL + time.Second)
	if strings.Contains(m.View(), "acked: org/repo#1") {
		t.Error("status should fade from the dashboard after statusTTL")
	}

	m.showLog = true
	v := m.View()
	if !strings.Contains(v, "recent log") || !strings.Contains(v, "acked: org/repo#1") {
		t.Error("the log view should retain the faded message")
	}
}

func TestLogIsBounded(t *testing.T) {
	st := &state.State{PRs: map[string]*derive.PR{}}
	m := New(st, "/dev/null", nil, func(string) bool { return false }, time.Minute)
	for range maxLogEntries + 50 {
		m.logLine("entry")
	}
	if len(m.events) != maxLogEntries {
		t.Errorf("log grew to %d entries, want cap at %d", len(m.events), maxLogEntries)
	}
}
