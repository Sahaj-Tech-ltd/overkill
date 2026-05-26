package session

import "errors"

var ErrNotFound = errors.New("session: not found")
var ErrExists = errors.New("session: already exists")
