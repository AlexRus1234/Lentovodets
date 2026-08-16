package testutil

import (
	"testing"
	"time"
)

func TestStepClock_AdvancesOneSecondPerCall(t *testing.T) {
	start := time.Unix(1700000000, 0)
	c := StepClock(start)

	for i, want := range []time.Time{
		start,
		start.Add(1 * time.Second),
		start.Add(2 * time.Second),
		start.Add(3 * time.Second),
	} {
		if got := c.Now(); !got.Equal(want) {
			t.Fatalf("вызов %d: got %v, want %v", i+1, got, want)
		}
	}
}
