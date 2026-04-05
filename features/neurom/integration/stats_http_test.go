package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/axsh/neurom/internal/bus"
	"github.com/axsh/neurom/internal/module"
	"github.com/axsh/neurom/internal/modules/monitor"
	"github.com/axsh/neurom/internal/modules/vram"
	"github.com/axsh/neurom/internal/stats"
	"github.com/axsh/neurom/internal/statsserver"
)

type httpAllResponse struct {
	VRAM struct {
		Commands map[string]stats.CommandStat `json:"commands"`
	} `json:"vram"`
	Monitor stats.MonitorStats `json:"monitor"`
}

type httpVRAMResponse struct {
	Commands map[string]stats.CommandStat `json:"commands"`
}

type httpMonitorResponse = stats.MonitorStats

func setupHTTPTestEnv(t *testing.T) (
	b bus.Bus,
	vramMod *vram.VRAMModule,
	mon *monitor.MonitorModule,
	ss *statsserver.StatsServer,
	cancel context.CancelFunc,
) {
	t.Helper()
	b = bus.NewChannelBus()
	mgr := module.NewManager(b)

	vramMod = vram.New()
	mon = monitor.New(monitor.MonitorConfig{Headless: true})

	mgr.Register(vramMod)
	mgr.Register(mon)

	ctx, cancelFn := context.WithCancel(context.Background())
	cancel = cancelFn

	if err := mgr.StartAll(ctx); err != nil {
		t.Fatalf("Failed to start modules: %v", err)
	}

	ss = statsserver.New(vramMod, mon, "0")
	if err := ss.Start(); err != nil {
		t.Fatalf("Failed to start stats server: %v", err)
	}

	t.Cleanup(func() {
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer shutCancel()
		ss.Shutdown(shutCtx)
		cancel()
		mgr.StopAll()
		b.Close()
	})

	return
}

func TestHTTPStatsIntegration(t *testing.T) {
	b, _, _, ss, _ := setupHTTPTestEnv(t)

	for range 10 {
		_ = b.Publish("vram", &bus.BusMessage{
			Target:    "draw_pixel",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		})
	}

	for range 5 {
		_ = b.Publish("vram", &bus.BusMessage{
			Target:    "set_palette",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0xFF, 0x00, 0x00},
		})
	}

	time.Sleep(1500 * time.Millisecond)

	url := fmt.Sprintf("http://%s/stats", ss.Addr())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /stats failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var result httpAllResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	dp, ok := result.VRAM.Commands["draw_pixel"]
	if !ok {
		t.Fatal("draw_pixel not found in VRAM commands")
	}
	if dp.Last10s.Count != 10 {
		t.Errorf("draw_pixel Last10s.Count = %d, want 10", dp.Last10s.Count)
	}

	sp, ok := result.VRAM.Commands["set_palette"]
	if !ok {
		t.Fatal("set_palette not found in VRAM commands")
	}
	if sp.Last10s.Count != 5 {
		t.Errorf("set_palette Last10s.Count = %d, want 5", sp.Last10s.Count)
	}

	if result.Monitor.FPS1s < 0 {
		t.Errorf("Monitor FPS1s = %f, expected >= 0", result.Monitor.FPS1s)
	}
}

func TestHTTPStatsVRAMOnly(t *testing.T) {
	b, _, _, ss, _ := setupHTTPTestEnv(t)

	for range 3 {
		_ = b.Publish("vram", &bus.BusMessage{
			Target:    "draw_pixel",
			Operation: bus.OpCommand,
			Data:      []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		})
	}

	time.Sleep(1500 * time.Millisecond)

	url := fmt.Sprintf("http://%s/stats/vram", ss.Addr())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /stats/vram failed: %v", err)
	}
	defer resp.Body.Close()

	var result httpVRAMResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	dp, ok := result.Commands["draw_pixel"]
	if !ok {
		t.Fatal("draw_pixel not found")
	}
	if dp.Last10s.Count != 3 {
		t.Errorf("draw_pixel Last10s.Count = %d, want 3", dp.Last10s.Count)
	}
}

func TestHTTPStatsMonitorOnly(t *testing.T) {
	_, _, _, ss, _ := setupHTTPTestEnv(t)

	time.Sleep(100 * time.Millisecond)

	url := fmt.Sprintf("http://%s/stats/monitor", ss.Addr())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET /stats/monitor failed: %v", err)
	}
	defer resp.Body.Close()

	var result httpMonitorResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode failed: %v", err)
	}

	if result.FPS1s < 0 {
		t.Errorf("Monitor FPS1s = %f, expected >= 0", result.FPS1s)
	}
}

func TestHTTPStats_StableEntries(t *testing.T) {
	b, _, _, ss, _ := setupHTTPTestEnv(t)

	_ = b.Publish("vram", &bus.BusMessage{
		Target: "draw_pixel", Operation: bus.OpCommand,
		Data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	})
	_ = b.Publish("vram", &bus.BusMessage{
		Target: "set_palette", Operation: bus.OpCommand,
		Data: []byte{0x00, 0xFF, 0x00, 0x00},
	})

	time.Sleep(1500 * time.Millisecond)

	url := fmt.Sprintf("http://%s/stats", ss.Addr())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()
	var r1 httpAllResponse
	json.NewDecoder(resp.Body).Decode(&r1)

	if _, ok := r1.VRAM.Commands["draw_pixel"]; !ok {
		t.Fatal("draw_pixel missing from first snapshot")
	}
	if _, ok := r1.VRAM.Commands["set_palette"]; !ok {
		t.Fatal("set_palette missing from first snapshot")
	}
	keyCount := len(r1.VRAM.Commands)

	_ = b.Publish("vram", &bus.BusMessage{
		Target: "draw_pixel", Operation: bus.OpCommand,
		Data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	})

	time.Sleep(1500 * time.Millisecond)

	resp2, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp2.Body.Close()
	var r2 httpAllResponse
	json.NewDecoder(resp2.Body).Decode(&r2)

	if _, ok := r2.VRAM.Commands["set_palette"]; !ok {
		t.Fatal("set_palette disappeared from second snapshot")
	}
	if len(r2.VRAM.Commands) != keyCount {
		t.Errorf("command key count changed: %d -> %d", keyCount, len(r2.VRAM.Commands))
	}
}

func TestHTTPStats_SnapshotIdempotent(t *testing.T) {
	b, _, _, ss, _ := setupHTTPTestEnv(t)

	_ = b.Publish("vram", &bus.BusMessage{
		Target: "draw_pixel", Operation: bus.OpCommand,
		Data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
	})

	time.Sleep(1500 * time.Millisecond)

	url := fmt.Sprintf("http://%s/stats/vram", ss.Addr())

	resp1, err := http.Get(url)
	if err != nil {
		t.Fatalf("first GET failed: %v", err)
	}
	defer resp1.Body.Close()
	var r1 httpVRAMResponse
	json.NewDecoder(resp1.Body).Decode(&r1)

	resp2, err := http.Get(url)
	if err != nil {
		t.Fatalf("second GET failed: %v", err)
	}
	defer resp2.Body.Close()
	var r2 httpVRAMResponse
	json.NewDecoder(resp2.Body).Decode(&r2)

	keys1 := make([]string, 0, len(r1.Commands))
	for k := range r1.Commands {
		keys1 = append(keys1, k)
	}
	keys2 := make([]string, 0, len(r2.Commands))
	for k := range r2.Commands {
		keys2 = append(keys2, k)
	}
	if len(keys1) != len(keys2) {
		t.Errorf("key count mismatch: %d vs %d", len(keys1), len(keys2))
	}

	for _, k := range keys1 {
		if r1.Commands[k].Last10s.Count != r2.Commands[k].Last10s.Count {
			t.Errorf("Last10s.Count mismatch for %s: %d vs %d",
				k, r1.Commands[k].Last10s.Count, r2.Commands[k].Last10s.Count)
		}
	}
}

func TestHTTPStats_MultiWindow(t *testing.T) {
	b, _, _, ss, _ := setupHTTPTestEnv(t)

	for range 5 {
		_ = b.Publish("vram", &bus.BusMessage{
			Target: "draw_pixel", Operation: bus.OpCommand,
			Data: []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x01},
		})
	}

	time.Sleep(1500 * time.Millisecond)

	url := fmt.Sprintf("http://%s/stats/vram", ss.Addr())
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	var raw map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)

	cmdRaw, ok := raw["commands"]
	if !ok {
		t.Fatal("commands field missing")
	}
	var cmds map[string]json.RawMessage
	json.Unmarshal(cmdRaw, &cmds)

	dpRaw, ok := cmds["draw_pixel"]
	if !ok {
		t.Fatal("draw_pixel missing")
	}

	var dp map[string]json.RawMessage
	json.Unmarshal(dpRaw, &dp)

	for _, key := range []string{"last_1s", "last_10s", "last_30s"} {
		if _, ok := dp[key]; !ok {
			t.Errorf("draw_pixel missing field %s", key)
		}
	}

	var dpStat stats.CommandStat
	json.Unmarshal(dpRaw, &dpStat)
	if dpStat.Last10s.Count < dpStat.Last1s.Count {
		t.Errorf("Last10s.Count (%d) < Last1s.Count (%d)",
			dpStat.Last10s.Count, dpStat.Last1s.Count)
	}
	if dpStat.Last30s.Count < dpStat.Last10s.Count {
		t.Errorf("Last30s.Count (%d) < Last10s.Count (%d)",
			dpStat.Last30s.Count, dpStat.Last10s.Count)
	}
}
