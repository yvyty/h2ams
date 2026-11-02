package ports

import "errors"

// listening is not implemented on Windows yet; status shows no ports there.
func listening([]int) (map[int][]int, error) {
	return nil, errors.ErrUnsupported
}
