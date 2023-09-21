package reconciler

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openconfig/gribigo/rib"
)

func TestLocalRIB(t *testing.T) {

	t.Run("get", func(t *testing.T) {
		l := &LocalRIB{
			r: rib.New("DEFAULT"),
		}

		want := rib.New("DEFAULT")
		got, err := l.Get()
		if err != nil {
			t.Fatalf("(*LocalRIB).Get(): did not get expected error, got: %v, want: nil", err)
		}

		if diff := cmp.Diff(got, want,
			cmpopts.EquateEmpty(), cmp.AllowUnexported(rib.RIB{}),
			cmpopts.IgnoreFields(rib.RIB{}, "nrMu", "pendMu", "ribCheck"),
			cmp.AllowUnexported(rib.RIBHolder{}),
			cmpopts.IgnoreFields(rib.RIBHolder{}, "mu", "refCounts", "checkFn"),
		); diff != "" {
			t.Fatalf("(*LocalRIB).Get(): did not get expected results, diff(-got,+want):\n%s", diff)
		}
	})

}
