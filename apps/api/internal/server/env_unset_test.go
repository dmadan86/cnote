package server_test

import (
	"os"
	"testing"
)

// unsetEnv removes a variable for the test (t.Setenv restores the original value afterwards).
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	_ = os.Unsetenv(key)
}
