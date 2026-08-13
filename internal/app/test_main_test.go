package app

import (
	"os"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestMain(m *testing.M) {
	originalPass, hadPass := os.LookupEnv("PIVOTFLOW_PASS")
	_ = os.Setenv("PIVOTFLOW_PASS", "test_password_123")
	gin.SetMode(gin.TestMode)

	code := m.Run()

	if hadPass {
		_ = os.Setenv("PIVOTFLOW_PASS", originalPass)
	} else {
		_ = os.Unsetenv("PIVOTFLOW_PASS")
	}
	os.Exit(code)
}
