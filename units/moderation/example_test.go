package moderation_test

import (
	"context"
	"fmt"
	"time"

	"github.com/ydnikolaev/go-foundation/units/moderation"
)

// memStore is a Store in memory. A real host binds the same methods to one database transaction.
type memStore struct {
	items   map[moderation.Target]moderation.Item
	reports []report
	mutes   []moderation.Mute
	log     []moderation.Entry
}

type report struct {
	moderation.Report
	status moderation.Status
}

func (s *memStore) Item(_ context.Context, t moderation.Target) (moderation.Item, error) {
	it, ok := s.items[t]
	if !ok {
		return moderation.Item{}, moderation.ErrNotFound
	}
	return it, nil
}

func (s *memStore) SetHidden(_ context.Context, t moderation.Target, h *moderation.Hiding) error {
	it := s.items[t]
	it.Hidden, it.HiddenReason = h != nil, ""
	if h != nil {
		it.HiddenReason = h.Reason
	}
	s.items[t] = it
	return nil
}

func (s *memStore) HasOpenReport(_ context.Context, reporterID string, t moderation.Target) (bool, error) {
	for _, r := range s.reports {
		if r.ReporterID == reporterID && r.Target == t && r.status == moderation.Open {
			return true, nil
		}
	}
	return false, nil
}

func (s *memStore) AddReport(_ context.Context, r moderation.Report) error {
	s.reports = append(s.reports, report{Report: r, status: moderation.Open})
	return nil
}

func (s *memStore) ResolveReports(_ context.Context, t moderation.Target, outcome moderation.Status, _ string, _ time.Time) (int, error) {
	n := 0
	for i := range s.reports {
		if s.reports[i].Target == t && s.reports[i].status == moderation.Open {
			s.reports[i].status = outcome
			n++
		}
	}
	return n, nil
}

func (s *memStore) ActiveMute(_ context.Context, userID string, now time.Time) (*moderation.Mute, error) {
	for i := range s.mutes {
		if s.mutes[i].UserID == userID && s.mutes[i].Active(now) {
			return &s.mutes[i], nil
		}
	}
	return nil, nil
}

func (s *memStore) Mute(_ context.Context, id string) (moderation.Mute, error) {
	for _, m := range s.mutes {
		if m.ID == id {
			return m, nil
		}
	}
	return moderation.Mute{}, moderation.ErrNotFound
}

func (s *memStore) AddMute(_ context.Context, m moderation.Mute) (string, error) {
	m.ID = fmt.Sprint(len(s.mutes) + 1)
	s.mutes = append(s.mutes, m)
	return m.ID, nil
}

func (s *memStore) SetLifted(_ context.Context, id string, at time.Time) error {
	for i := range s.mutes {
		if s.mutes[i].ID == id {
			s.mutes[i].LiftedAt = &at
		}
	}
	return nil
}

func (s *memStore) Append(_ context.Context, e moderation.Entry) error {
	s.log = append(s.log, e)
	return nil
}

// Example files a report on a post, hides the post as a moderator and gives its author a one-day mute. Each call
// runs on one Store; the log keeps the hide and the mute, each with its actor and reason.
func Example() {
	ctx := context.Background()
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	post := moderation.Target{Kind: moderation.KindPost, ID: "p1"}
	s := &memStore{items: map[moderation.Target]moderation.Item{post: {Target: post, AuthorID: "alice"}}}

	err := moderation.FileReport(ctx, s, moderation.Report{Target: post, ReporterID: "bob", Reason: "spam", At: now})
	fmt.Println("report:", err)

	_, changed, _ := moderation.Hide(ctx, s, post, moderation.Hiding{Reason: "spam", ActorID: "mod", At: now})
	fmt.Println("hidden:", changed, "reports:", s.reports[0].status)

	end := now.Add(24 * time.Hour)
	m, _ := moderation.Issue(ctx, s, moderation.Order{
		UserID: "alice", Step: moderation.Period, Reason: "spam", Until: &end, ActorID: "mod",
	}, now)
	fmt.Println("muted:", m.Active(now), "log:", s.log[0].Kind, s.log[1].Kind)
	// Output:
	// report: <nil>
	// hidden: true reports: actioned
	// muted: true log: hide mute
}
