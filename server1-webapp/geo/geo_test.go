package geo_test

import (
	"net/http/httptest"
	"testing"

	"mission1_cdn/server1-webapp/geo"
)

func TestDetectClientRegion_QueryOverride(t *testing.T) {
	req := httptest.NewRequest("GET", "/cdn/img_123/banner.png?region=Asia", nil)
	info := geo.DetectClientRegion(req)
	if info.Region != "Asia" || info.NearestNodeID != "sg-1" {
		t.Errorf("Expected Asia/sg-1, got %s/%s", info.Region, info.NearestNodeID)
	}

	reqNA := httptest.NewRequest("GET", "/cdn/img_123/banner.png?region=North%20America", nil)
	infoNA := geo.DetectClientRegion(reqNA)
	if infoNA.Region != "North America" || infoNA.NearestNodeID != "ny-1" {
		t.Errorf("Expected North America/ny-1, got %s/%s", infoNA.Region, infoNA.NearestNodeID)
	}

	reqEU := httptest.NewRequest("GET", "/cdn/img_123/banner.png?region=Europe", nil)
	infoEU := geo.DetectClientRegion(reqEU)
	if infoEU.Region != "Europe" || infoEU.NearestNodeID != "ldn-1" {
		t.Errorf("Expected Europe/ldn-1, got %s/%s", infoEU.Region, infoEU.NearestNodeID)
	}
}

func TestDetectClientRegion_HeaderOverride(t *testing.T) {
	req := httptest.NewRequest("GET", "/cdn/img_123/banner.png", nil)
	req.Header.Set("X-Client-Region", "Europe")
	info := geo.DetectClientRegion(req)
	if info.Region != "Europe" || info.NearestNodeID != "ldn-1" {
		t.Errorf("Expected Europe/ldn-1, got %s/%s", info.Region, info.NearestNodeID)
	}
}

func TestDetectClientRegion_IPHeuristics(t *testing.T) {
	// Loopback / Private -> Asia
	reqLocal := httptest.NewRequest("GET", "/cdn/img_123/banner.png", nil)
	reqLocal.RemoteAddr = "127.0.0.1:45321"
	infoLocal := geo.DetectClientRegion(reqLocal)
	if infoLocal.Region != "Asia" || infoLocal.NearestNodeID != "sg-1" {
		t.Errorf("Expected local to default to Asia/sg-1, got %s/%s", infoLocal.Region, infoLocal.NearestNodeID)
	}

	// European IP (RIPE block e.g. 185.x)
	reqEU := httptest.NewRequest("GET", "/cdn/img_123/banner.png", nil)
	reqEU.Header.Set("X-Forwarded-For", "185.199.108.153")
	infoEU := geo.DetectClientRegion(reqEU)
	if infoEU.Region != "Europe" || infoEU.NearestNodeID != "ldn-1" {
		t.Errorf("Expected Europe/ldn-1 for 185.x, got %s/%s", infoEU.Region, infoEU.NearestNodeID)
	}

	// Asian IP (APNIC block e.g. 202.x)
	reqAsia := httptest.NewRequest("GET", "/cdn/img_123/banner.png", nil)
	reqAsia.Header.Set("X-Forwarded-For", "202.12.27.33")
	infoAsia := geo.DetectClientRegion(reqAsia)
	if infoAsia.Region != "Asia" || infoAsia.NearestNodeID != "sg-1" {
		t.Errorf("Expected Asia/sg-1 for 202.x, got %s/%s", infoAsia.Region, infoAsia.NearestNodeID)
	}
}
