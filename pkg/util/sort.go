package util

import (
	"time"
)

// SliceSortedByTime keeps data that should be sorted by KeyTime
type SliceSortedByTime[T any] []structSortedByTime[T]

// NewSliceToSortByTime creates a new SliceSortedByTime
func NewSliceToSortByTime[T any]() SliceSortedByTime[T] {
	return SliceSortedByTime[T]{}
}

type structSortedByTime[T any] struct {
	KeyTime time.Time
	Data    T
}

// Add adds a new element to the end of the slice.
// Call sort.Sort() on the slice to have it ordered.
func (s *SliceSortedByTime[T]) Add(t time.Time, data T) {
	*s = append(*s, structSortedByTime[T]{
		KeyTime: t,
		Data:    data,
	})
}

// Len implements sort.Interface
func (s SliceSortedByTime[T]) Len() int {
	return len(s)
}

// Less implements sort.Interface
func (s SliceSortedByTime[T]) Less(i, j int) bool {
	return s[i].KeyTime.Before(s[j].KeyTime)
}

// Swap implements sort.Interface
func (s SliceSortedByTime[T]) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
