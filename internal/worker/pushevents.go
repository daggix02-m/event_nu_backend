package worker

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/daggix02-m/event_nu_backend/internal/config"
	"github.com/daggix02-m/event_nu_backend/internal/domain"
	"github.com/daggix02-m/event_nu_backend/internal/infrastructure/push"
	"github.com/daggix02-m/event_nu_backend/internal/repository"
	seg "github.com/segmentio/kafka-go"
)

// followerReader is the fanout's view of who follows an organizer.
type followerReader interface {
	FollowerIDs(ctx context.Context, organizerID string) ([]string, error)
}

// deviceFinder is the fanout's view of a user's registered devices.
type deviceFinder interface {
	ListByUser(ctx context.Context, userID string) ([]*domain.Device, error)
}

// devicePruner removes dead registration tokens after a provider rejection.
type devicePruner interface {
	DeleteByToken(ctx context.Context, token string) error
}

// eventFetcher resolves the event's title for the notification body.
type eventFetcher interface {
	GetByID(ctx context.Context, id string) (*domain.Event, error)
}

// publishedFact mirrors the relay's event.published payload.
type publishedFact struct {
	EventID     string `json:"event_id"`
	OrganizerID string `json:"organizer_id"`
}

// EventPublishedFanout is a Kafka consumer group that turns event.published
// facts into push notifications for the organizer's followers. Offsets are
// committed AFTER processing, so a crash mid-handling replays the fact on
// restart (at-least-once); processing errors are logged and the offset moves
// on anyway — a permanently broken fact must not wedge the group (same
// posture as the relay's poison-row handling).
type EventPublishedFanout struct {
	logger  *slog.Logger
	cfg     config.Config
	follows followerReader
	devices deviceFinder
	pruner  devicePruner
	events  eventFetcher
	pusher  push.Provider
}

func NewEventPublishedFanout(logger *slog.Logger, cfg config.Config, follows followerReader, devices deviceFinder, pruner devicePruner, events eventFetcher, pusher push.Provider) *EventPublishedFanout {
	return &EventPublishedFanout{logger: logger, cfg: cfg, follows: follows, devices: devices, pruner: pruner, events: events, pusher: pusher}
}

func (f *EventPublishedFanout) Run(ctx context.Context) error {
	reader := seg.NewReader(seg.ReaderConfig{
		Brokers: f.cfg.KafkaBrokers,
		GroupID: f.cfg.KafkaPushGroup,
		Topic:   f.cfg.KafkaTopicEvents,
		// Only new facts matter to notifications: a group joining replays
		// historical publishes otherwise, pushing users months-old events.
		StartOffset: seg.LastOffset,
		Dialer:      nil,
	})
	defer reader.Close()
	f.logger.Info("event published fanout joined", "group", f.cfg.KafkaPushGroup, "topic", f.cfg.KafkaTopicEvents)
	for {
		msg, err := reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // shutdown, not failure
			}
			f.logger.Error("fanout read failed", "error", err.Error())
			continue
		}
		f.handlePublish(ctx, msg)
		if err := reader.CommitMessages(ctx, msg); err != nil {
			f.logger.Error("fanout offset commit failed", "error", err.Error())
		}
	}
}

func (f *EventPublishedFanout) handlePublish(ctx context.Context, msg seg.Message) {
	var fact publishedFact
	if err := json.Unmarshal(msg.Value, &fact); err != nil {
		f.logger.Error("fanout unparsable fact", "topic", msg.Topic, "error", err.Error())
		return
	}

	title := "An event was published"
	if ev, err := f.events.GetByID(ctx, fact.EventID); err != nil {
		f.logger.Warn("fanout event lookup failed", "event_id", fact.EventID, "error", err.Error())
	} else if ev.Title != "" {
		// Only adopt a real title: a found-but-untitled event (deleted or
		// renamed mid-flight) keeps the placeholder instead of pushing "".
		title = ev.Title
	}

	followerIDs, err := f.follows.FollowerIDs(ctx, fact.OrganizerID)
	if err != nil {
		f.logger.Error("fanout follower lookup failed", "organizer_id", fact.OrganizerID, "error", err.Error())
		return
	}

	sent, dead := 0, 0
	for _, uid := range followerIDs {
		devices, err := f.devices.ListByUser(ctx, uid)
		if err != nil {
			f.logger.Error("fanout device lookup failed", "user_id", uid, "error", err.Error())
			continue
		}
		for _, d := range devices {
			message := push.Message{
				Token: d.Token,
				Title: title,
				Body:  "Check it out in the upcoming tab.",
				Data:  map[string]string{"event_id": fact.EventID, "organizer_id": fact.OrganizerID},
			}
			if _, err := f.pusher.Send(ctx, message); err != nil {
				var perr *push.Error
				if asPushError(err, &perr) && perr.IsInvalidToken() {
					if derr := f.pruner.DeleteByToken(ctx, d.Token); derr != nil {
						f.logger.Warn("fanout device prune failed", "token", d.Token, "error", derr.Error())
					}
					dead++
				} else if perr != nil && perr.Retryable() {
					// At-least-once on the fact level means a provider
					// hiccup for one device is not replayed wholesale;
					// the reminder queue is the retry surface for sends.
					f.logger.Warn("fanout push retryable failure", "token", d.Token, "error", err.Error())
				} else {
					f.logger.Error("fanout push failed", "token", d.Token, "error", err.Error())
				}
				continue
			}
			sent++
		}
	}
	f.logger.Info("event.published fanned out",
		"event_id", fact.EventID, "followers", len(followerIDs), "pushed", sent, "pruned", dead)
}

// asPushError unwraps so fanout logic can react to provider semantics
// without switching on strings.
func asPushError(err error, target **push.Error) bool {
	if e, ok := err.(*push.Error); ok {
		*target = e
		return true
	}
	*target = nil
	return false
}

// Compile-time proofs: the consumer wraps the same repositories the push
// consumer uses.
var _ followerReader = (*repository.FollowRepository)(nil)
var _ deviceFinder = (*repository.DeviceRepository)(nil)
var _ devicePruner = (*repository.DeviceRepository)(nil)
var _ eventFetcher = (*repository.EventRepository)(nil)
