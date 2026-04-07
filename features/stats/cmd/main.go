package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"
)

type windowStats struct {
	Count int64 `json:"count"`
	AvgNs int64 `json:"avg_ns"`
	MaxNs int64 `json:"max_ns"`
}

type commandStat struct {
	Last1s  windowStats `json:"last_1s"`
	Last10s windowStats `json:"last_10s"`
	Last30s windowStats `json:"last_30s"`
	EmaNs   int64       `json:"ema_ns"`
}

type vramStats struct {
	Commands map[string]commandStat `json:"commands"`
}

type monitorStats struct {
	FPS1s  float64 `json:"fps_1s"`
	FPS10s float64 `json:"fps_10s"`
	FPS30s float64 `json:"fps_30s"`
}

type statsResponse struct {
	VRAM    vramStats    `json:"vram"`
	Monitor monitorStats `json:"monitor"`
}

func main() {
	endpoint := flag.String("endpoint", "http://localhost:8080/stats", "Stats HTTP endpoint URL")
	watch := flag.Bool("watch", false, "Continuously refresh stats every second")
	flag.Parse()

	for {
		resp, err := fetchStats(*endpoint)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			if !*watch {
				os.Exit(1)
			}
			time.Sleep(time.Second)
			continue
		}

		if *watch {
			fmt.Print("\033[H\033[2J")
		}
		printStats(resp)

		if !*watch {
			return
		}
		time.Sleep(time.Second)
	}
}

func fetchStats(endpoint string) (*statsResponse, error) {
	resp, err := http.Get(endpoint)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var s statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode failed: %w", err)
	}
	return &s, nil
}

func printStats(s *statsResponse) {
	fmt.Println("=== VRAM Stats ===")
	fmt.Printf("%-24s ──── 1s ────   ──── 10s ────   ──── 30s ────   ── EMA ──\n", "Command")
	fmt.Printf("%-24s %5s %7s  %5s %7s   %5s %7s   %7s\n",
		"", "Count", "Avg(μs)", "Count", "Avg(μs)", "Count", "Avg(μs)", "Avg(μs)")
	fmt.Println("──────────────────────────────────────────────────────────────────────────────────")

	names := make([]string, 0, len(s.VRAM.Commands))
	for name := range s.VRAM.Commands {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		st := s.VRAM.Commands[name]
		fmt.Printf("%-24s %5d %7.2f  %5d %7.2f   %5d %7.2f   %7.2f\n",
			name,
			st.Last1s.Count, float64(st.Last1s.AvgNs)/1000.0,
			st.Last10s.Count, float64(st.Last10s.AvgNs)/1000.0,
			st.Last30s.Count, float64(st.Last30s.AvgNs)/1000.0,
			float64(st.EmaNs)/1000.0,
		)
	}

	fmt.Println()
	fmt.Println("=== Monitor Stats ===")
	fmt.Printf("FPS (1s): %.1f   FPS (10s): %.1f   FPS (30s): %.1f\n",
		s.Monitor.FPS1s, s.Monitor.FPS10s, s.Monitor.FPS30s)
}
