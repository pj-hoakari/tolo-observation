// Package application contains use cases for the greet context.
package application

import (
	"context"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
	"github.com/pj-hoakari/tolo-observation/internal/repository"
)

// GreetInput contains the values accepted by the Greet use case.
type GreetInput struct {
	Name string
}

// GreetUseCase builds a greeting for the named caller.
type GreetUseCase interface {
	Greet(context.Context, GreetInput) (domain.Greeting, error)
}

// GreetUseCases groups the greet operations exposed by the Connect transport.
type GreetUseCases interface {
	GreetUseCase
}

// GreetService implements greet use cases.
type GreetService struct {
	greetings repository.GreetingRepository
}

func NewGreetService(greetings repository.GreetingRepository) *GreetService {
	return &GreetService{greetings: greetings}
}

func (s *GreetService) Greet(ctx context.Context, input GreetInput) (domain.Greeting, error) {
	greeting, err := domain.NewGreeting(input.Name)
	if err != nil {
		return domain.Greeting{}, err
	}

	if err := s.greetings.Record(ctx, greeting); err != nil {
		return domain.Greeting{}, err
	}

	return greeting, nil
}
