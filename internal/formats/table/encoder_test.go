package table

import (
	"flag"
	"testing"

	"github.com/anchore/syft/internal/formats/common/testutils"
	"github.com/anchore/syft/syft/format"
	"github.com/anchore/syft/syft/source"
	"github.com/go-test/deep"
)

var updateTableGoldenFiles = flag.Bool("update-table", false, "update the *.golden files for table format")

func TestTablePresenter(t *testing.T) {
	catalog, metadata, distro := testutils.DirectoryInput(t)
	testutils.AssertPresenterAgainstGoldenSnapshot(t,
		format.NewPresenter(encoder, catalog, &metadata, distro, source.SquashedScope),
		*updateTableGoldenFiles,
	)
}

func TestRemoveDuplicateRows(t *testing.T) {
	data := [][]string{
		{"1", "2", "3"},
		{"a", "b", "c"},
		{"1", "2", "3"},
		{"a", "b", "c"},
		{"1", "2", "3"},
		{"4", "5", "6"},
		{"1", "2", "1"},
	}

	expected := [][]string{
		{"1", "2", "3"},
		{"a", "b", "c"},
		{"4", "5", "6"},
		{"1", "2", "1"},
	}

	actual := removeDuplicateRows(data)

	if diffs := deep.Equal(expected, actual); len(diffs) > 0 {
		t.Errorf("found diffs!")
		for _, d := range diffs {
			t.Errorf("   diff: %+v", d)
		}
	}

}
