package health

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSetLogger(t *testing.T) {
	l := new(voidLogger)
	SetLogger(l)

	assert.Equal(t, l, logger)
}

func TestRegisterChecker(t *testing.T) {
	c1 := new(MockChecker)
	c2 := new(MockChecker)
	RegisterChecker(c1, c2)

	assert.Equal(t, []Checker{c1, c2}, checkers)
}

func TestSetTimeout(t *testing.T) {
	d := 10 * time.Second
	SetTimeout(d)

	assert.Equal(t, d, timeout)
}

func TestHandlerFunc(t *testing.T) {
	tests := []struct {
		name               string
		checkers           []Checker
		expectedStatusCode int
	}{
		{
			name: "Successful",
			checkers: []Checker{
				&MockChecker{
					HealthCheckMocks: []MockChecker_HealthCheckMock{
						{OutError: nil},
					},
				},
				&MockChecker{
					HealthCheckMocks: []MockChecker_HealthCheckMock{
						{OutError: nil},
					},
				},
			},
			expectedStatusCode: 200,
		},
		{
			name: "Unsuccessful",
			checkers: []Checker{
				&MockChecker{
					HealthCheckMocks: []MockChecker_HealthCheckMock{
						{OutError: nil},
					},
				},
				&MockChecker{
					HealthCheckMocks: []MockChecker_HealthCheckMock{
						{OutError: errors.New("failed to check")},
					},
				},
			},
			expectedStatusCode: 503,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logger = new(voidLogger)
			checkers = tc.checkers
			timeout = time.Second

			req := httptest.NewRequest("GET", "/", nil)
			rec := httptest.NewRecorder()
			handler := HandlerFunc()
			handler(rec, req)

			assert.Equal(t, tc.expectedStatusCode, rec.Result().StatusCode)
		})
	}
}

// -------------------------------------------------- Mock Types --------------------------------------------------

type (
	MockChecker struct {
		StringOutString string

		HealthCheckIndex int
		HealthCheckMocks []MockChecker_HealthCheckMock
	}

	MockChecker_HealthCheckMock struct {
		InCtx    context.Context
		OutError error
	}
)

func (m *MockChecker) String() string {
	return m.StringOutString
}

func (m *MockChecker) HealthCheck(ctx context.Context) error {
	if m.HealthCheckIndex >= len(m.HealthCheckMocks) {
		panic("HealthCheck called more times than expected")
	}

	i := m.HealthCheckIndex
	m.HealthCheckIndex++

	m.HealthCheckMocks[i].InCtx = ctx

	return m.HealthCheckMocks[i].OutError
}
