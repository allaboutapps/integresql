package util_test

import (
	"sort"
	"testing"
	"time"

	"github.com/allaboutapps/integresql/pkg/util"
	"github.com/stretchr/testify/assert"
)

func TestSliceSortedByTimeImplements(t *testing.T) {
	assert.Implements(t, (*sort.Interface)(nil), new(util.SliceSortedByTime[int]))
}

func TestSliceSortedByTimeSorted(t *testing.T) {
	s := util.NewSliceToSortByTime[int]()
	s.Add(time.Now().Add(time.Hour), 1)
	s.Add(time.Now().Add(-time.Hour), 2)
	s.Add(time.Now(), 3)

	sort.Sort(s)
	assert.Equal(t, s[0].Data, 2, s[0].KeyTime)
	assert.Equal(t, s[1].Data, 3, s[1].KeyTime)
	assert.Equal(t, s[2].Data, 1, s[2].KeyTime)
}
