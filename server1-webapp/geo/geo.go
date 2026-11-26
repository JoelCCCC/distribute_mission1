package geo

import (
	"net"
	"net/http"
	"strings"
)

// RegionInfo represents detected region and nearest node assignment.
type RegionInfo struct {
	ClientIP      string `json:"client_ip"`
	Region        string `json:"region"`
	NearestNodeID string `json:"nearest_node_id"`
	Description   string `json:"description"`
}

// DetectClientRegion inspects the request to determine client IP and mapped geographic region.
func DetectClientRegion(r *http.Request) RegionInfo {
	// 1. Check for manual override query parameter or header (ideal for demos and testing)
	if override := r.URL.Query().Get("region"); override != "" {
		return fromRegionName(getClientIP(r), override, "Manual override query parameter")
	}
	if override := r.Header.Get("X-Client-Region"); override != "" {
		return fromRegionName(getClientIP(r), override, "Manual override header")
	}

	clientIP := getClientIP(r)
	ip := net.ParseIP(clientIP)

	// If localhost / loopback / private IP, default to Asia (Singapore)
	if ip == nil || ip.IsLoopback() || isPrivateIP(ip) {
		return RegionInfo{
			ClientIP:      clientIP,
			Region:        "Asia",
			NearestNodeID: "sg-1",
			Description:   "Local/Private network detected - Defaulting to Asia (Singapore)",
		}
	}

	// Basic geographic heuristics based on first octet of IPv4
	region := resolveRegionFromIP(ip)
	return fromRegionName(clientIP, region, "IP geolocation heuristic")
}

func fromRegionName(clientIP, region, desc string) RegionInfo {
	lower := strings.ToLower(strings.TrimSpace(region))
	switch {
	case strings.Contains(lower, "asia") || strings.Contains(lower, "sg") || strings.Contains(lower, "singapore") || strings.Contains(lower, "tokyo") || strings.Contains(lower, "japan"):
		return RegionInfo{ClientIP: clientIP, Region: "Asia", NearestNodeID: "sg-1", Description: desc}
	case strings.Contains(lower, "north america") || strings.Contains(lower, "america") || strings.Contains(lower, "us") || strings.Contains(lower, "ny") || strings.Contains(lower, "new york"):
		return RegionInfo{ClientIP: clientIP, Region: "North America", NearestNodeID: "ny-1", Description: desc}
	case strings.Contains(lower, "europe") || strings.Contains(lower, "eu") || strings.Contains(lower, "uk") || strings.Contains(lower, "ldn") || strings.Contains(lower, "london"):
		return RegionInfo{ClientIP: clientIP, Region: "Europe", NearestNodeID: "ldn-1", Description: desc}
	default:
		return RegionInfo{ClientIP: clientIP, Region: "Asia", NearestNodeID: "sg-1", Description: desc}
	}
}

func getClientIP(r *http.Request) string {
	// Check standard proxy headers
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	if cfip := r.Header.Get("CF-Connecting-IP"); cfip != "" {
		return strings.TrimSpace(cfip)
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func isPrivateIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		// 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
		return ip4[0] == 10 ||
			(ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31) ||
			(ip4[0] == 192 && ip4[1] == 168)
	}
	return false
}

func resolveRegionFromIP(ip net.IP) string {
	ip4 := ip.To4()
	if ip4 == nil {
		return "Asia"
	}

	firstOctet := ip4[0]
	// Broad Regional Internet Registry (RIR) octet blocks for testing:
	// APNIC (Asia-Pacific): e.g. 1, 14, 27, 36, 42, 43, 49, 58-61, 101, 103, 106, 110-126, 175, 180, 182, 183, 202, 203, 210, 211, 218-223
	// RIPE (Europe): e.g. 2, 31, 46, 62, 77-95, 109, 141, 145, 151, 176, 178, 185, 188, 193-195, 212, 213, 217
	// ARIN (North America): e.g. 3, 4, 8, 12, 13, 15, 16, 23, 24, 32, 34, 35, 38, 40, 44, 45, 47, 50, 52, 54, 63-76, 96-100, 104, 107, 108, 128-140, 142-144, 146-150, 152-174, 184, 192, 198, 199, 204-209
	switch {
	case (firstOctet >= 77 && firstOctet <= 95) || firstOctet == 46 || firstOctet == 62 || firstOctet == 176 || firstOctet == 178 || firstOctet == 185 || firstOctet == 188 || firstOctet == 193 || firstOctet == 194 || firstOctet == 195 || firstOctet == 212 || firstOctet == 213 || firstOctet == 217:
		return "Europe"
	case (firstOctet >= 58 && firstOctet <= 61) || (firstOctet >= 110 && firstOctet <= 126) || firstOctet == 1 || firstOctet == 27 || firstOctet == 42 || firstOctet == 101 || firstOctet == 103 || firstOctet == 175 || firstOctet == 180 || firstOctet == 182 || firstOctet == 183 || firstOctet == 202 || firstOctet == 203 || firstOctet == 210 || firstOctet == 211 || (firstOctet >= 218 && firstOctet <= 223):
		return "Asia"
	default:
		return "North America"
	}
}
