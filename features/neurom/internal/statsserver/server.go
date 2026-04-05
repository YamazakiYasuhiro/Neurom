package statsserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"

	"github.com/axsh/neurom/internal/stats"
)

// VRAMStatsProvider provides VRAM performance statistics.
type VRAMStatsProvider interface {
	GetStats() map[string]stats.CommandStat
}

// MonitorStatsProvider provides Monitor performance statistics.
type MonitorStatsProvider interface {
	GetMonitorStats() stats.MonitorStats
}

type vramResponse struct {
	Commands map[string]stats.CommandStat `json:"commands"`
}

type allResponse struct {
	VRAM    vramResponse     `json:"vram"`
	Monitor stats.MonitorStats `json:"monitor"`
}

// StatsServer serves performance stats over HTTP.
type StatsServer struct {
	vram     VRAMStatsProvider
	monitor  MonitorStatsProvider
	server   *http.Server
	listener net.Listener
}

// New creates a StatsServer that listens on the given port.
// Use port "0" for OS-assigned port (useful in tests).
func New(vram VRAMStatsProvider, monitor MonitorStatsProvider, port string) *StatsServer {
	s := &StatsServer{
		vram:    vram,
		monitor: monitor,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /stats", s.handleAll)
	mux.HandleFunc("GET /stats/vram", s.handleVRAM)
	mux.HandleFunc("GET /stats/monitor", s.handleMonitor)

	s.server = &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}
	return s
}

// Start begins listening and serving in the background.
// The actual listening address is available via Addr() after Start returns.
func (s *StatsServer) Start() error {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return err
	}
	s.listener = ln
	go s.server.Serve(ln)
	return nil
}

// Addr returns the listener address (useful when port "0" is used).
func (s *StatsServer) Addr() string {
	if s.listener != nil {
		return s.listener.Addr().String()
	}
	return s.server.Addr
}

// Shutdown gracefully shuts down the server.
func (s *StatsServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

func (s *StatsServer) handleAll(w http.ResponseWriter, r *http.Request) {
	resp := allResponse{
		VRAM:    vramResponse{Commands: s.vram.GetStats()},
		Monitor: s.monitor.GetMonitorStats(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *StatsServer) handleVRAM(w http.ResponseWriter, r *http.Request) {
	resp := vramResponse{Commands: s.vram.GetStats()}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *StatsServer) handleMonitor(w http.ResponseWriter, r *http.Request) {
	resp := s.monitor.GetMonitorStats()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
