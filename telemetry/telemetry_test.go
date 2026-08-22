package telemetry

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSingleton(t *testing.T) {
	assert.NotNil(t, singleton)

	var p Probe

	t.Run("SetProbe", func(t *testing.T) {
		p = new(probe)
		SetProbe(p)
	})

	t.Run("GetProbe", func(t *testing.T) {
		assert.Equal(t, p, GetProbe())
	})
}
