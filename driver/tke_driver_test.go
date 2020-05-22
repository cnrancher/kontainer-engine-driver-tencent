package driver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetBoolean(t *testing.T) {
	tests := []struct {
		num  int64
		want bool
	}{
		{
			num:  0,
			want: false,
		},
		{
			num:  1,
			want: true,
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, getBoolean(tt.num))
	}

}

func TestGetInstanceChargeType(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{
			value: "PayByHour",
			want:  "POSTPAID_BY_HOUR",
		},
		{
			value: "PayByMonth",
			want:  "PREPAID",
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, getInstanceChargeType(tt.value))
	}

}

func TestGetInternetChargeType(t *testing.T) {
	tests := []struct {
		value string
		want  string
	}{
		{
			value: "PayByHour",
			want:  "BANDWIDTH_POSTPAID_BY_HOUR",
		},
		{
			value: "PayByTraffic",
			want:  "TRAFFIC_POSTPAID_BY_HOUR",
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, getInternetChargeType(tt.value))
	}
}
