package google

import "errors"

var (
	ErrUnauthenticated = errors.New("unauthenticated")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidArgument  = errors.New("invalid argument")
)
