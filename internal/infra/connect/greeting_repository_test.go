package connect

import (
	"context"

	"github.com/pj-hoakari/tolo-observation/internal/domain"
)

// nopGreetingRepository satisfies repository.GreetingRepository for transport
// tests, which assert transport behavior rather than persistence.
type nopGreetingRepository struct{}

func (nopGreetingRepository) Record(context.Context, domain.Greeting) error { return nil }
