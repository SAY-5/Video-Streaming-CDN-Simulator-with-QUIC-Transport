package routing

import "math"

// HaversineKm returns the great-circle distance in kilometers between two
// lat/lng points (in degrees) on a spherical Earth.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusKm * c
}

// EstimateRTTMs converts a great-circle distance into an estimated RTT in ms.
// The formula doubles the propagation time (round trip) and applies a routing
// overhead factor of 2 to account for switching, queueing, and non-great-circle
// paths.
func EstimateRTTMs(distanceKm float64) float64 {
	// Light in fiber ~= 200_000 km/s => 0.005 ms per km one-way.
	oneWayMs := distanceKm * 0.005
	return oneWayMs * 2 * 2
}

func toRad(deg float64) float64 { return deg * math.Pi / 180 }
