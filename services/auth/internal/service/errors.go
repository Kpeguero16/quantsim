package service

import "errors"

// ErrDuplicateUser is returned by Register when the email or username is
// already taken. Handlers map this to 409.
var ErrDuplicateUser = errors.New("email or username already exists")
