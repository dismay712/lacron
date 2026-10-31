package lacron

import (
	"context"
	"testing"
	"time"
)

func TestSecondScheduleMatchesAndPreview(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, 10, 28, 12, 0, 0, 500000000, loc)
	for _, expr := range []string{"* * * * * *", "*/7 * * * * *", "3,17,59 */2 * * * *", "* * * * *", "0 0 * * * *"} {
		s, err := ParseSchedule(expr)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 5; i++ {
			next := s.Next(after)
			// A second-by-second reference proves Next skips no valid time.
			want := after.Truncate(time.Second).Add(time.Second)
			for !s.Matches(want) {
				want = want.Add(time.Second)
			}
			if !next.Equal(want) || !s.Matches(next) {
				t.Fatalf("%s after %s: next %s, want %s", expr, after, next, want)
			}
			after = next.Add(123 * time.Millisecond)
		}
	}
}

func TestSecondScheduleDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ expr, after, want string }{
		{"5 30 2 * * *", "2026-03-08T00:00:00-05:00", "2026-03-09T02:30:05-04:00"},
		{"5 30 1 * * *", "2026-11-01T01:30:05-04:00", "2026-11-01T01:30:05-05:00"},
	} {
		s, _ := ParseSchedule(tc.expr)
		after, _ := time.Parse(time.RFC3339, tc.after)
		if got := s.Next(after.In(loc)).Format(time.RFC3339); got != tc.want {
			t.Fatalf("%s: next %s, want %s", tc.expr, got, tc.want)
		}
	}
}

func TestSchedulerEditsPauseAndDedup(t *testing.T) {
	s, ctx := testService(t)
	in := testInput()
	in.Schedule = "*/2 * * * * *"
	j := saveTestJob(t, s, in)
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, s.location)
	dispatch := func(at time.Time, count int) {
		t.Helper()
		s.dispatchDue(ctx, at)
		runs := s.Runs(j.ID)
		if len(runs) != count {
			t.Fatalf("at %s: got %d runs, want %d", at, len(runs), count)
		}
		if count > 0 {
			waitRun(t, s, runs[0].ID)
		}
	}
	dispatch(now, 1)
	dispatch(now.Add(500*time.Millisecond), 1) // No duplicate after fast completion.
	dispatch(now.Add(time.Second), 1)
	dispatch(now.Add(2*time.Second), 2)
	in.Enabled = false
	if _, err := s.Save(j.ID, in); err != nil {
		t.Fatal(err)
	}
	dispatch(now.Add(4*time.Second), 2)
	in.Enabled, in.Schedule = true, "5 * * * * *"
	if _, err := s.Save(j.ID, in); err != nil {
		t.Fatal(err)
	}
	dispatch(now.Add(4*time.Second), 2)
	dispatch(now.Add(5*time.Second), 3)
	dispatch(now.Add(65*time.Second), 4) // Skip missed minutes instead of replaying.
	dispatch(now.Add(5*time.Second), 4)  // Backwards clock cannot replay an old tick.
	if err := s.Delete(j.ID); err != nil {
		t.Fatal(err)
	}
	dispatch(now.Add(125*time.Second), 4)
}

func TestLiveSecondSchedulerAndShutdown(t *testing.T) {
	s, parent := testService(t)
	in := testInput()
	in.Schedule = "* * * * * *"
	j := saveTestJob(t, s, in)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.schedule(ctx) }()
	deadline := time.Now().Add(4 * time.Second)
	for len(s.Runs(j.ID)) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler did not stop")
	}
	runs := s.Runs(j.ID)
	if len(runs) < 2 {
		t.Fatalf("second scheduler did not fire twice: %+v", runs)
	}
	if runs[0].Source != "cron" || runs[0].StartedAt.Sub(runs[1].StartedAt) >= 2*time.Second {
		t.Fatalf("not second-resolution scheduling: %+v", runs)
	}
}
