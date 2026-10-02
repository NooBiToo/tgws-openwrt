package proxy

import (
	"testing"
	"time"
)

func TestFailTracker(t *testing.T) {
	now := time.Unix(1000, 0)
	f := newFailTracker(func() time.Time { return now }, time.Minute)

	if f.blocked("2") {
		t.Fatal("a fresh key must not be blocked")
	}
	f.cooldown("2")
	if !f.blocked("2") {
		t.Fatal("cooldown must block")
	}
	now = now.Add(59 * time.Second)
	if !f.blocked("2") {
		t.Fatal("still inside the cooldown")
	}
	now = now.Add(2 * time.Second)
	if f.blocked("2") {
		t.Fatal("the cooldown must expire — a transient failure is not forever")
	}

	f.blacklist("4")
	now = now.Add(24 * time.Hour)
	if !f.blocked("4") {
		t.Fatal("a blacklist must not expire")
	}
	f.clear("4")
	if !f.blocked("4") {
		t.Fatal("clear drops only a cooldown, not a blacklist")
	}
}
