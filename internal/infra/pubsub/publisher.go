package pubsub

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-googlecloud/v2/pkg/googlecloud"
	"github.com/ThreeDotsLabs/watermill-nats/v2/pkg/jetstream"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/nats-io/nats.go"
	natsjetstream "github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

const (
	GuestStatusTopic   = "guest-status"
	EventIDMetadataKey = "event_id"
)

type GuestStatusPublisher struct {
	publisher message.Publisher
}

func NewGuestStatusPublisher(publisher message.Publisher) *GuestStatusPublisher {
	return &GuestStatusPublisher{publisher: publisher}
}

func (p *GuestStatusPublisher) PublishGuestStatus(ctx context.Context, status domain.GuestStatus) error {
	payload, err := proto.Marshal(GuestStatusProto(status))
	if err != nil {
		return fmt.Errorf("encode guest status: %w", err)
	}

	msg := message.NewMessage(watermill.NewUUID(), payload)
	msg.Metadata.Set(EventIDMetadataKey, status.EventID)
	msg.SetContext(ctx)

	if err := p.publisher.Publish(GuestStatusTopic, msg); err != nil {
		return fmt.Errorf("publish guest status: %w", err)
	}

	return nil
}

func NewNATSPublisher(ctx context.Context, url string) (message.Publisher, error) {
	conn, err := nats.Connect(url)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}

	js, err := natsjetstream.New(conn)
	if err == nil {
		_, err = js.CreateOrUpdateStream(ctx, natsjetstream.StreamConfig{
			Name:     GuestStatusTopic,
			Subjects: []string{GuestStatusTopic},
		})
	}

	if err != nil {
		conn.Close()

		return nil, fmt.Errorf("provision the %s stream: %w", GuestStatusTopic, err)
	}

	publisher, err := jetstream.NewPublisher(jetstream.PublisherConfig{
		URL:             "",
		Conn:            conn,
		Logger:          watermill.NewSlogLogger(slog.Default()),
		ConfigureStream: nil,
		TrackMessageID:  false,
	})
	if err != nil {
		conn.Close()

		return nil, fmt.Errorf("create NATS JetStream publisher: %w", err)
	}

	return publisher, nil
}

func NewGooglePublisher(projectID string) (message.Publisher, error) {
	publisher, err := googlecloud.NewPublisher(googlecloud.PublisherConfig{
		ProjectID:                 projectID,
		DoNotCheckTopicExistence:  false,
		DoNotCreateTopicIfMissing: false,
		EnableMessageOrdering:     true,
		EnableMessageOrderingAutoResumePublishOnError: true,
		ConnectTimeout:  0,
		PublishTimeout:  0,
		PublishSettings: nil,
		ClientOptions:   nil,
		ClientConfig:    nil,
		Marshaler: googlecloud.NewOrderingMarshaler(func(_ string, msg *message.Message) (string, error) {
			return msg.Metadata.Get(EventIDMetadataKey), nil
		}),
	}, watermill.NewSlogLogger(slog.Default()))
	if err != nil {
		return nil, fmt.Errorf("create Cloud Pub/Sub publisher: %w", err)
	}

	return publisher, nil
}
