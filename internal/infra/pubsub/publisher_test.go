package pubsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	natsjetstream "github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/pj-hoakari/tolo-observation/internal/infra/pubsub"
)

const natsImage = "nats:2-alpine@sha256:7ae34c25dfd6fe2e01c6483eabef20d796690fdcac6c4d94ca780a6cf9877fde"

func TestNATSPublisherSetsTheMaxAgeOnTheStreamEvenWhenTheRelayCreatedItFirst(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := testcontainers.Run(ctx, natsImage,
		testcontainers.WithCmd("-js"),
		testcontainers.WithExposedPorts("4222/tcp"),
		testcontainers.WithWaitStrategy(wait.ForLog("Server is ready"), wait.ForListeningPort("4222/tcp")),
	)
	testcontainers.CleanupContainer(t, container)

	if err != nil {
		t.Fatalf("start NATS: %v", err)
	}

	url, err := container.PortEndpoint(ctx, "4222/tcp", "nats")
	if err != nil {
		t.Fatalf("PortEndpoint: %v", err)
	}

	conn, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect to NATS: %v", err)
	}

	t.Cleanup(conn.Close)

	js, err := natsjetstream.New(conn)
	if err != nil {
		t.Fatalf("open JetStream: %v", err)
	}

	topic := pubsub.GuestStatusTopic
	if _, err := js.CreateStream(ctx, natsjetstream.StreamConfig{Name: topic, Subjects: []string{topic}}); err != nil {
		t.Fatalf("create the %s stream as the relay does: %v", topic, err)
	}

	publisher, err := pubsub.NewNATSPublisher(ctx, url, 90*time.Minute)
	if err != nil {
		t.Fatalf("NewNATSPublisher: %v", err)
	}

	t.Cleanup(func() {
		if err := publisher.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	stream, err := js.Stream(ctx, topic)
	if err != nil {
		t.Fatalf("look up the %s stream: %v", topic, err)
	}

	if got := stream.CachedInfo().Config.MaxAge; got != 90*time.Minute {
		t.Errorf("%s stream MaxAge = %v, want 1h30m", topic, got)
	}
}
