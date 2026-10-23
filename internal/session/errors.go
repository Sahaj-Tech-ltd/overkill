package session

import "errors"

var ErrNotFound = errors.New("session: not found")
var ErrExists = errors.New("session: already exists")
var ErrConflict = errors.New("session: concurrent write conflict — reload before retrying")
