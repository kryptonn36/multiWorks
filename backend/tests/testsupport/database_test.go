package testsupport

import "testing"

func TestRejectDevelopmentDatabase(t *testing.T) {
	for _, dsn := range []string{"", "postgres://localhost/storyboard", "postgres://localhost/multiworks", "postgres://localhost/multiworks_test?dbname=storyboard"} {
		if ValidateDSN(dsn) == nil {
			t.Fatalf("unsafe test DSN accepted: %q", dsn)
		}
	}
	for _, dsn := range []string{"postgres://localhost/multiworks_test", "postgres://localhost/test_multiworks", "postgresql://localhost/multiworks_tests"} {
		if err := ValidateDSN(dsn); err != nil {
			t.Fatal(err)
		}
	}
}
