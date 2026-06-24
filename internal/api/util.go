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
