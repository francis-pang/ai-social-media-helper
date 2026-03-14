package batch

// FilterByIndexRange returns a subset of m for indices [baseIdx, baseIdx+count).
// Generic: works with any map[int]T.
func FilterByIndexRange(m map[int]string, baseIdx, count int) map[int]string {
	if m == nil || count == 0 {
		return m
	}
	out := make(map[int]string)
	for i := 0; i < count; i++ {
		if v, ok := m[baseIdx+i]; ok {
			out[baseIdx+i] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
