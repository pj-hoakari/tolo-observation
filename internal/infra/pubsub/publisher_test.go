package pubsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/nats-io/nats.go"
	natsjetstream "github.com/nats-io/nats.go/jetstream"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
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

type recordingPublisher struct {
	messages []*message.Message
}

func (p *recordingPublisher) Publish(_ string, messages ...*message.Message) error {
	p.messages = append(p.messages, messages...)

	return nil
}

func (p *recordingPublisher) Close() error {
	return nil
}

func TestPublishGuestStatusCarriesTheTraceContextInTheMetadata(t *testing.T) {
	previous := otel.GetTextMapPropagator()

	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() { otel.SetTextMapPropagator(previous) })

	traceID := trace.TraceID{0x4b, 0xf9, 0x2f, 0x35, 0x77, 0xb3, 0x4d, 0xa6, 0xa3, 0xce, 0x92, 0x9d, 0x0e, 0x0e, 0x47, 0x36}
	spanID := trace.SpanID{0x00, 0xf0, 0x67, 0xaa, 0x0b, 0xa9, 0x02, 0xb7}
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     spanID,
		TraceFlags: trace.FlagsSampled,
		TraceState: trace.TraceState{},
		Remote:     false,
	}))

	recorder := &recordingPublisher{messages: nil}
	status := domain.GuestStatus{EventID: "event-1", Sequence: 1, TakenAt: time.Unix(0, 0), Crowd: nil, Queues: nil}

	if err := pubsub.NewGuestStatusPublisher(recorder).PublishGuestStatus(ctx, status); err != nil {
		t.Fatalf("PublishGuestStatus: %v", err)
	}

	if len(recorder.messages) != 1 {
		t.Fatalf("published %d messages, want 1", len(recorder.messages))
	}

	want := "00-" + traceID.String() + "-" + spanID.String() + "-01"
	if got := recorder.messages[0].Metadata.Get("traceparent"); got != want {
		t.Errorf("traceparent = %q, want %q", got, want)
	}
}
