package integration

import (
	"testing"

	"github.com/soundmap/soundmap/gen/go/geo/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestQueryHotspots_ValidRequest(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	req := &geov1.QueryHotspotsRequest{
		Center: &geov1.GeoPoint{
			Latitude:  40.7128,
			Longitude: -74.0060,
		},
		RadiusKm:   5.0,
		Resolution: 8,
		Limit:      10,
		MinDecibel: 0.0,
	}

	resp, err := geoClient.QueryHotspots(ctx, req)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, resp.QueryTimeMs, float32(0))
	// Zero results is valid if no data exists
	assert.NotNil(t, resp.Cells)
}

func TestGetHexCellStats_ValidH3(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	req := &geov1.GetHexCellStatsRequest{
		H3Index:          "8928308280fffff",
		TimeRangeHours:   24,
	}

	resp, err := geoClient.GetHexCellStats(ctx, req)
	assert.NoError(t, err)
	assert.NotNil(t, resp.Cell)
}

func TestQueryHotspots_RadiusTooLarge(t *testing.T) {
	t.Parallel()
	ctx := newAuthCtx(t)

	req := &geov1.QueryHotspotsRequest{
		Center: &geov1.GeoPoint{
			Latitude:  40.7128,
			Longitude: -74.0060,
		},
		RadiusKm:   100.0, // Too large
		Resolution: 8,
	}

	_, err := geoClient.QueryHotspots(ctx, req)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}
