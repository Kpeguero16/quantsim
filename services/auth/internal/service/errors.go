package service

import "errors"

// ErrDuplicateUser is returned by Register when the email or username is
// already taken. Handlers map this to 409.
var ErrDuplicateUser = errors.New("email or username already exists")

// ErrInvalidCredentials is returned by Login for both an unknown email and a
// wrong password -- the caller can't distinguish the two, so the API doesn't
// leak whether an email is registered. Handlers map this to 401.
var ErrInvalidCredentials = errors.New("invalid email or password")

// ErrTokenInvalid is returned by Refresh for any token problem: expired,
// malformed, wrong signature, or the wrong token type presented. Handlers
// map this to 401.
var ErrTokenInvalid = errors.New("invalid or expired token")

// ErrUserNotFound is returned by Me when the token is valid but the user row
// is gone (e.g. deleted after the token was issued). Handlers map this to
// 401, not 404 -- same failure vocabulary as every other /auth/me rejection.
var ErrUserNotFound = errors.New("user not found")
