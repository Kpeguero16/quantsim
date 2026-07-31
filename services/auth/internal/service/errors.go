package service

import "errors"

// ErrDuplicateUser is returned by Register when the email or username is
// already taken. Handlers map this to 409.
var ErrDuplicateUser = errors.New("email or username already exists")

// ErrInvalidCredentials is returned by Login for both an unknown email and a
// wrong password -- the caller can't distinguish the two, so the API doesn't
// leak whether an email is registered. Handlers map this to 401.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrInvalidInput is returned by Register when the submitted email,
// username, or password fails the registration rules. It's wrapped with a
// specific, user-facing message per rule (see validate.go) -- callers match
// with errors.Is while the message stays precise. Handlers map this to 400
// invalid_request and render the message verbatim.
//
// Deliberately NOT returned by Login: applying the registration rules there
// would lock out every account that predates them, and a policy-specific
// message would be distinguishable from the uniform ErrInvalidCredentials,
// turning login into a user-enumeration oracle. See SPEC.md 2.12.
var ErrInvalidInput = errors.New("invalid input")

// ErrTokenInvalid is returned by Refresh for any token problem: expired,
// malformed, wrong signature, or the wrong token type presented. Handlers
// map this to 401.
var ErrTokenInvalid = errors.New("invalid or expired token")

// ErrUserNotFound is the canonical "no such user" outcome from a store
// lookup by email or ID -- distinct from any other store error (a DB
// connection failure, a timeout) so callers can tell "doesn't exist" apart
// from "the store is broken" and only the former gets folded into an
// auth-failure response. Used by Login (-> ErrInvalidCredentials), Refresh
// (-> ErrTokenInvalid), and Me, which returns it directly (401, not 404 --
// same failure vocabulary as every other /auth/me rejection).
var ErrUserNotFound = errors.New("user not found")
