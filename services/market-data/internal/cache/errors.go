package cache

import "errors"

// ErrNotFound is returned by GetPrice when the key is missing or expired.
// The service layer maps this to service.ErrPriceNotCached.
var ErrNotFound = errors.New("cache: key not found")
