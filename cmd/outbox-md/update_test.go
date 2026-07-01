package main

import (
	"testing"
	"time"
)

func TestSemverNewer(t *testing.T) {
	cases := []struct {
		cur, lat string
		want     bool
	}{
		{"0.6.0", "0.7.0", true},
		{"0.6.0", "0.6.0", false},  // equal is not newer
		{"0.7.0", "0.6.0", false},  // older latest
		{"0.9.0", "0.10.0", true},  // numeric, not lexical (0.10 > 0.9)
		{"0.10.0", "0.9.0", false}, // and the reverse
		{"1.0.0", "0.99.0", false}, // major dominates
		{"v0.6.0", "v0.7.0", true}, // leading v tolerated
		{"0.6.0", "0.7.0-rc1", true},
		{"dev", "0.6.0", false},     // dev never self-updates
		{"0.6.0", "garbage", false}, // unparseable latest → not newer
		{"0.6.0", "0.6", false},     // wrong arity → not newer
	}
	for _, c := range cases {
		if got := semverNewer(c.cur, c.lat); got != c.want {
			t.Errorf("semverNewer(%q, %q) = %v, want %v", c.cur, c.lat, got, c.want)
		}
	}
}

func TestClassifyPath(t *testing.T) {
	cases := []struct {
		path   string
		docker bool
		want   installKind
	}{
		{"/anywhere", true, kindDocker}, // container wins over path
		{"/opt/homebrew/Cellar/outbox-md/0.6.0/bin/outbox", false, kindHomebrew},
		{"/usr/local/Cellar/outbox-md/0.6.0/bin/outbox", false, kindHomebrew},
		{"/opt/homebrew/bin/outbox", false, kindHomebrew},
		{"/usr/local/bin/outbox", false, kindStandalone},
		{"/home/u/.local/bin/outbox", false, kindStandalone},
	}
	for _, c := range cases {
		if got := classifyPath(c.path, c.docker); got != c.want {
			t.Errorf("classifyPath(%q, docker=%v) = %v, want %v", c.path, c.docker, got, c.want)
		}
	}
}

func TestThrottleDue(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if !throttleDue(time.Time{}, now, 24*time.Hour) {
		t.Error("zero last-check should be due")
	}
	if throttleDue(now.Add(-time.Hour), now, 24*time.Hour) {
		t.Error("1h ago with a 24h interval should NOT be due")
	}
	if !throttleDue(now.Add(-25*time.Hour), now, 24*time.Hour) {
		t.Error("25h ago with a 24h interval should be due")
	}
}
