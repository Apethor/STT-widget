package transcript

import (
	"context"
	"fmt"
	"sync"
)

type admissionKey struct{}

type requestAdmission struct {
	mu         sync.Mutex
	foreground context.Context
	budget     *recoveryBudget
	first      bool
}

func withAdmission(ctx, foreground context.Context, budget *recoveryBudget) context.Context {
	return context.WithValue(ctx, admissionKey{}, &requestAdmission{
		foreground: foreground, budget: budget, first: true,
	})
}

// CheckAdmission is called immediately before each physical provider send.
// A sent Cloud call can finish under the service's lifetime, but a semantic
// retry must still belong to an active foreground request and its budget.
func CheckAdmission(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	admission, _ := ctx.Value(admissionKey{}).(*requestAdmission)
	if admission == nil {
		return nil
	}
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if err := admission.foreground.Err(); err != nil {
		return err
	}
	if admission.first {
		// The outer attempt has already reserved its first request.
		admission.first = false
		return nil
	}
	if admission.budget != nil && !admission.budget.take() {
		return fmt.Errorf("a recuperação atingiu seu limite de chamadas: %w", ErrOutputLimit)
	}
	return nil
}
