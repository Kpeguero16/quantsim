package service

import "errors"

// ErrDuplicateUser is returned by Register when the email or username is
// already taken. Handlers map this to 409.
var ErrDuplicateUser = errors.New("email or username already exists")

// ErrInvalidCredentials is returned by Login for both an unknown email and a
// wrong password -- the caller can't distinguish the two, so the API doesn't
// leak whether an email is registered. Handlers map this to 401.
var ErrInvalidCredentials = errors.New("invalid email or password")
