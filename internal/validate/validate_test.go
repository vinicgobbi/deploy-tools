package validate

import "testing"

func TestAppName(t *testing.T) {
	for _, ok := range []string{"docs", "minha-api", "a_b", "A1"} {
		if err := AppName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../etc", "a b", "a.b", "ä"} {
		if AppName(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestReleaseID(t *testing.T) {
	for _, ok := range []string{"20260911153000-a99d021", "1.2.3", "v1_0"} {
		if err := ReleaseID(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".", "..", "a/b", "../x", "a b", "a;b"} {
		if ReleaseID(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
