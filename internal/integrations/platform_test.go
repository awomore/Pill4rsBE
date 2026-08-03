package integrations

import (
	"reflect"
	"testing"
)

func TestGenderValues(t *testing.T) {
	cases := []struct {
		in   any
		want []string
	}{
		{[]string{"female"}, []string{"female"}},
		{[]string{"male"}, []string{"male"}},
		{[]string{"female", "male"}, []string{"male", "female"}},
		{[]string{"F"}, []string{"female"}},
		{[]int{2}, []string{"female"}},
		{[]int{1, 2}, []string{"male", "female"}},
		{[]any{"all"}, nil},
		{[]any{"women", "men"}, []string{"male", "female"}},
		{nil, nil},
		{"female", []string{"female"}},
	}
	for _, c := range cases {
		if got := GenderValues(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("GenderValues(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestMetaGenderCodes(t *testing.T) {
	if got := MetaGenderCodes([]string{"female"}); !reflect.DeepEqual(got, []int{2}) {
		t.Errorf("MetaGenderCodes(female) = %v, want [2]", got)
	}
	if got := MetaGenderCodes([]string{"male"}); !reflect.DeepEqual(got, []int{1}) {
		t.Errorf("MetaGenderCodes(male) = %v, want [1]", got)
	}
	// both genders = unaffected targeting => omit to let Meta target everyone.
	if got := MetaGenderCodes([]string{"male", "female"}); got != nil {
		t.Errorf("MetaGenderCodes(male+female) = %v, want nil", got)
	}
	if got := MetaGenderCodes(nil); got != nil {
		t.Errorf("MetaGenderCodes(nil) = %v, want nil", got)
	}
}

func TestValidCreativeFormat(t *testing.T) {
	for _, f := range []string{"image", "video", "gif", "audio", "playable"} {
		if !ValidCreativeFormat(f) {
			t.Errorf("expected %q to be valid", f)
		}
	}
	if ValidCreativeFormat("hologram") {
		t.Error("hologram should not be a valid format")
	}
}
