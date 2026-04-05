package statsserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/axsh/neurom/internal/stats"
)

type mockVRAMStats struct {
	data map[string]stats.CommandStat
}

func (m *mockVRAMStats) GetStats() map[string]stats.CommandStat { return m.data }

type mockMonitorStats struct {
	ms stats.MonitorStats
}

func (m *mockMonitorStats) GetMonitorStats() stats.MonitorStats { return m.ms }

func setupServer(vram *mockVRAMStats, mon *mockMonitorStats) *StatsServer {
	return New(vram, mon, "0")
}

func TestHandleStats(t *testing.T) {
	vram := &mockVRAMStats{data: map[string]stats.CommandStat{
		"draw_pixel": {
			Last1s:  stats.WindowStats{Count: 100, AvgNs: 5000, MaxNs: 12000},
			Last10s: stats.WindowStats{Count: 950, AvgNs: 4800, MaxNs: 15000},
			Last30s: stats.WindowStats{Count: 2900, AvgNs: 4900, MaxNs: 15000},
		},
		"clear_vram": {
			Last1s:  stats.WindowStats{Count: 10, AvgNs: 50000, MaxNs: 80000},
			Last10s: stats.WindowStats{Count: 95, AvgNs: 48000, MaxNs: 80000},
			Last30s: stats.WindowStats{Count: 280, AvgNs: 49000, MaxNs: 80000},
		},
	}}
	mon := &mockMonitorStats{ms: stats.MonitorStats{FPS1s: 60.0, FPS10s: 58.5, FPS30s: 57.2}}
	s := setupServer(vram, mon)

	req := httptest.NewRequest("GET", "/stats", nil)
	w := httptest.NewRecorder()
	s.handleAll(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp allResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.VRAM.Commands["draw_pixel"].Last1s.Count != 100 {
		t.Errorf("draw_pixel Last1s.Count = %d, want 100", resp.VRAM.Commands["draw_pixel"].Last1s.Count)
	}
	if resp.Monitor.FPS1s != 60.0 {
		t.Errorf("FPS1s = %f, want 60.0", resp.Monitor.FPS1s)
	}
	if resp.Monitor.FPS10s != 58.5 {
		t.Errorf("FPS10s = %f, want 58.5", resp.Monitor.FPS10s)
	}
	if resp.Monitor.FPS30s != 57.2 {
		t.Errorf("FPS30s = %f, want 57.2", resp.Monitor.FPS30s)
	}
}

func TestHandleStatsVRAM(t *testing.T) {
	vram := &mockVRAMStats{data: map[string]stats.CommandStat{
		"blit_rect": {
			Last1s:  stats.WindowStats{Count: 50, AvgNs: 10000, MaxNs: 20000},
			Last10s: stats.WindowStats{Count: 480, AvgNs: 9800, MaxNs: 22000},
			Last30s: stats.WindowStats{Count: 1400, AvgNs: 9900, MaxNs: 22000},
		},
	}}
	mon := &mockMonitorStats{}
	s := setupServer(vram, mon)

	req := httptest.NewRequest("GET", "/stats/vram", nil)
	w := httptest.NewRecorder()
	s.handleVRAM(w, req)

	var resp vramResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.Commands["blit_rect"].Last1s.Count != 50 {
		t.Errorf("blit_rect Last1s.Count = %d, want 50", resp.Commands["blit_rect"].Last1s.Count)
	}
}

func TestHandleStatsMonitor(t *testing.T) {
	vram := &mockVRAMStats{data: map[string]stats.CommandStat{}}
	mon := &mockMonitorStats{ms: stats.MonitorStats{FPS1s: 30.0, FPS10s: 28.5, FPS30s: 27.0}}
	s := setupServer(vram, mon)

	req := httptest.NewRequest("GET", "/stats/monitor", nil)
	w := httptest.NewRecorder()
	s.handleMonitor(w, req)

	var resp stats.MonitorStats
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if resp.FPS1s != 30.0 {
		t.Errorf("FPS1s = %f, want 30.0", resp.FPS1s)
	}
	if resp.FPS10s != 28.5 {
		t.Errorf("FPS10s = %f, want 28.5", resp.FPS10s)
	}
	if resp.FPS30s != 27.0 {
		t.Errorf("FPS30s = %f, want 27.0", resp.FPS30s)
	}
}

func TestHandleStatsContentType(t *testing.T) {
	vram := &mockVRAMStats{data: map[string]stats.CommandStat{}}
	mon := &mockMonitorStats{}
	s := setupServer(vram, mon)

	endpoints := []struct {
		path    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"/stats", s.handleAll},
		{"/stats/vram", s.handleVRAM},
		{"/stats/monitor", s.handleMonitor},
	}

	for _, ep := range endpoints {
		t.Run(ep.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", ep.path, nil)
			w := httptest.NewRecorder()
			ep.handler(w, req)

			ct := w.Header().Get("Content-Type")
			if ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

func TestHandleStatsEmptyData(t *testing.T) {
	vram := &mockVRAMStats{data: map[string]stats.CommandStat{}}
	mon := &mockMonitorStats{}
	s := setupServer(vram, mon)

	req := httptest.NewRequest("GET", "/stats", nil)
	w := httptest.NewRecorder()
	s.handleAll(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp allResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if len(resp.VRAM.Commands) != 0 {
		t.Errorf("expected empty commands, got %d", len(resp.VRAM.Commands))
	}
	if resp.Monitor.FPS1s != 0 {
		t.Errorf("FPS1s = %f, want 0", resp.Monitor.FPS1s)
	}
}
