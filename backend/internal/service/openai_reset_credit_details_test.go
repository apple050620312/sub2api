//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIResetCreditExplicitZeroIsKnownButPartialDataIsNot(t *testing.T) {
	for _, scenario := range []struct {
		body    string
		known   bool
		invalid bool
	}{
		{`{"available_count":0}`, true, false},
		{`[]`, true, false},
		{`{"available_count":1}`, false, false},
		{`{}`, false, false},
		{`{"available_count":0,"credits":"invalid"}`, false, true},
	} {
		t.Run(scenario.body, func(t *testing.T) {
			details, err := parseOpenAIRateLimitResetCreditDetails([]byte(scenario.body))
			if scenario.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, scenario.known, details.ObservationComplete)
		})
	}
}
