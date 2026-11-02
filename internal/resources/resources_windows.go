package resources

import "errors"

// sample is not implemented on Windows yet; status shows no CPU or memory.
func (s *Sampler) sample([]int) (map[int]Usage, error) {
	return nil, errors.ErrUnsupported
}
