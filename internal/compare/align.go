package compare

// match pairs an index of the reference with an index of the candidate. An index is -1 on the side that lacks the element.
type match struct {
	reference int
	candidate int
}

// align aligns two sequences with a longest common subsequence.
func align[T any](reference []T, candidate []T, equal func(T, T) bool) []match {
	n, m := len(reference), len(candidate)

	// lengths[i][j] is the length of the longest common subsequence of reference[i:] and candidate[j:].
	lengths := make([][]int, n+1)
	for i := range lengths {
		lengths[i] = make([]int, m+1)
	}

	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			switch {
			case equal(reference[i], candidate[j]):
				lengths[i][j] = lengths[i+1][j+1] + 1

			case lengths[i+1][j] >= lengths[i][j+1]:
				lengths[i][j] = lengths[i+1][j]

			default:
				lengths[i][j] = lengths[i][j+1]
			}
		}
	}

	var matches []match

	i, j := 0, 0
	for i < n && j < m {
		switch {
		case equal(reference[i], candidate[j]):
			matches = append(matches, match{reference: i, candidate: j})
			i++
			j++

		case lengths[i+1][j] >= lengths[i][j+1]:
			matches = append(matches, match{reference: i, candidate: -1})
			i++

		default:
			matches = append(matches, match{reference: -1, candidate: j})
			j++
		}
	}

	for ; i < n; i++ {
		matches = append(matches, match{reference: i, candidate: -1})
	}

	for ; j < m; j++ {
		matches = append(matches, match{reference: -1, candidate: j})
	}

	return matches
}
