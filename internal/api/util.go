package api

func ref[T any](v T) *T {
	return &v
}

func deref[T any](v *T) T {
	if v == nil {
		var zero T
		return zero
	}

	return *v
}

// refNonZero returns a pointer to v, or nil if v is the zero value.
func refNonZero[T comparable](v T) *T {
	var zero T

	if v == zero {
		return nil
	}

	return &v
}
