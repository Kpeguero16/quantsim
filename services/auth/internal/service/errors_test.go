package service_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/kpeguero/quantsim/services/auth/internal/service"
)

// TestInvalidInputMessage pins the contract the handler depends on: what
// comes back is rendered verbatim to the user, so the sentinel's own text
// ("invalid input:") must never survive into it.
//
// Handler tests cover this indirectly by asserting the rendered message
// lacks that prefix. They do not reach the two edge cases below, both of
// which are deliberate behaviour rather than accident.
func TestInvalidInputMessage(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "wrapped sentinel yields the message alone",
			err:  fmt.Errorf("%w: password must be at least 15 characters", service.ErrInvalidInput),
			want: "password must be at least 15 characters",
		},
		{
			name: "nil yields empty",
			err:  nil,
			want: "",
		},
		{
			// Not a silent empty string: a miswired caller should get
			// something diagnosable rather than a blank error region.
			name: "unrelated error is returned in full",
			err:  errors.New("something else went wrong"),
			want: "something else went wrong",
		},
		{
			// The bare sentinel has no ": " to trim, so TrimPrefix is a
			// no-op and its own text is all there is. Pinned so the
			// behaviour is a decision rather than a surprise.
			name: "bare sentinel is returned unchanged",
			err:  service.ErrInvalidInput,
			want: "invalid input",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := service.InvalidInputMessage(tc.err); got != tc.want {
				t.Fatalf("InvalidInputMessage(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
