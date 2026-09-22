//go:generate go tool mockgen -source=../repository/greeting.go -destination=mock_greeting_repository_test.go -package=application_test

package application_test

import (
	"context"
	"errors"
	"testing"

	"github.com/pj-hoakari/tolo-observation/internal/application"
	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"go.uber.org/mock/gomock"
)

func TestGreet(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	greetings := NewMockGreetingRepository(ctrl)
	greetings.EXPECT().Record(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, greeting domain.Greeting) error {
		if got, want := greeting.Name(), "Ada"; got != want {
			t.Errorf("recorded greeting name = %q, want %q", got, want)
		}

		return nil
	})

	service := application.NewGreetService(greetings)

	greeting, err := service.Greet(context.Background(), application.GreetInput{Name: "Ada"})
	if err != nil {
		t.Fatalf("Greet() error = %v", err)
	}

	if got, want := greeting.Message(), "Hello, Ada!"; got != want {
		t.Errorf("Message() = %q, want %q", got, want)
	}
}

func TestGreetValidatesInput(t *testing.T) {
	t.Parallel()

	// No Record expectation: an invalid greeting must never be recorded.
	service := application.NewGreetService(NewMockGreetingRepository(gomock.NewController(t)))

	_, err := service.Greet(context.Background(), application.GreetInput{})
	if !errors.Is(err, domain.ErrGreetingNameRequired) {
		t.Errorf("Greet() error = %v, want %v", err, domain.ErrGreetingNameRequired)
	}
}

func TestGreetPropagatesRecordError(t *testing.T) {
	t.Parallel()

	errRecord := errors.New("record unavailable")

	ctrl := gomock.NewController(t)
	greetings := NewMockGreetingRepository(ctrl)
	greetings.EXPECT().Record(gomock.Any(), gomock.Any()).Return(errRecord)

	service := application.NewGreetService(greetings)

	_, err := service.Greet(context.Background(), application.GreetInput{Name: "Ada"})
	if !errors.Is(err, errRecord) {
		t.Errorf("Greet() error = %v, want %v", err, errRecord)
	}
}
