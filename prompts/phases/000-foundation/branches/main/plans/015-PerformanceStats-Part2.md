# 015-PerformanceStats-Part2

> **Source Specification**: `prompts/phases/000-foundation/ideas/main/015-PerformanceStats.md`
> **Part 1**: `prompts/phases/000-foundation/plans/main/015-PerformanceStats-Part1.md`

## Goal Description

Monitor モジュールに FPS 計測を追加し、HTTP スタットエンドポイント (`--stats-port`) と Stats CLI (`features/stats`) を実装する。Part 1 で作成した `stats.Collector` を活用する。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R4: Monitor `onPaint` フレームカウント | Proposed Changes > monitor.go |
| R4: 1秒ウィンドウごとの FPS 保持 | Proposed Changes > monitor.go (stats.Collector 使用) |
| R4: Headless でも計測 (`receiveLoop` メッセージカウント) | Proposed Changes > monitor.go |
| R5: `monitor` トピック Subscribe 追加 | Proposed Changes > monitor.go |
| R5: `get_stats` → `stats_data` 応答 (JSON) | Proposed Changes > monitor.go |
| R5: `GetFPS()` / `GetFrameCount()` / `GetMonitorStats()` エクスポートメソッド | Proposed Changes > monitor.go |
| R6: `--stats-port` 起動オプション | Proposed Changes > main.go |
| R6: HTTP `GET /stats`, `/stats/vram`, `/stats/monitor` | Proposed Changes > server.go |
| R6: HTTP サーバーの graceful shutdown | Proposed Changes > server.go |
| R6: VRAMStatsProvider / MonitorStatsProvider インターフェース | Proposed Changes > server.go |
| R6: レスポンス JSON フォーマット | Proposed Changes > server.go |
| R7: Stats CLI `--endpoint`, `--watch` フラグ | Proposed Changes > features/stats/main.go |
| R7: テーブル形式出力 | Proposed Changes > features/stats/main.go |
| R7: `--watch` ANSI クリア上書き表示 | Proposed Changes > features/stats/main.go |

## Proposed Changes

### internal/modules/monitor

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor_test.go](file://features/neurom/internal/modules/monitor/monitor_test.go)

*   **Description**: FPS カウンター + `get_stats` コマンドの単体テスト追加。
*   **Test Cases**:

    | Test Name | Purpose |
    | :--- | :--- |
    | `TestMonitorFPSCounter` | stats.Collector の `nowFunc` を制御し、`RecordFrame()` を N 回呼び出した後にウィンドウ回転をトリガー。`GetMonitorStats()` で FPS = N が返ることを検証 |
    | `TestMonitorGetStatsCommand` | `monitor` トピックに `get_stats` を送信し、`monitor_update` トピックに `stats_data` が発行されることを検証。Data を JSON パースし `fps` と `frames_last_sec` キーが存在することを確認 |
    | `TestMonitorHeadlessMessageCount` | Headless モードで `receiveLoop` にメッセージを送信。ウィンドウ回転後に `GetMonitorStats()` の `FramesLastSec` がメッセージ数を反映していることを検証 |

*   **Technical Design**:
    ```go
    func TestMonitorFPSCounter(t *testing.T) {
        b := &dummyBus{ch: make(chan *bus.BusMessage, 10)}
        m := New(MonitorConfig{Headless: true})
        m.bus = b

        // m.stats.nowFunc を差し替え
        // m.RecordFrame() を 60 回呼び出し
        // nowFunc を 1秒以上進め、もう1回 RecordFrame() でウィンドウ回転
        // stats := m.GetMonitorStats()
        // assert stats.FramesLastSec == 60
    }
    ```

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: FPS 計測ロジック + `monitor` トピック Subscribe + `get_stats` コマンドハンドラ追加。
*   **Technical Design**:

    ```go
    import (
        // ... existing ...
        "encoding/json"
        "github.com/axsh/neurom/internal/stats"
    )

    // MonitorStats holds the monitor performance statistics.
    type MonitorStats struct {
        FPS           float64 `json:"fps"`
        FramesLastSec int64   `json:"frames_last_sec"`
    }

    type MonitorModule struct {
        // ... existing fields ...
        stats *stats.Collector  // NEW
    }

    func New(cfg MonitorConfig) *MonitorModule {
        m := &MonitorModule{
            // ... existing initialization ...
            stats: stats.NewCollector(),  // NEW
        }
        // ...
        return m
    }

    // RecordFrame records a single frame for FPS counting.
    // Called from onPaint (non-headless) or receiveLoop (headless).
    func (m *MonitorModule) RecordFrame()
    // Logic: m.stats.Record("frame", 0)

    // GetMonitorStats returns the monitor performance statistics.
    func (m *MonitorModule) GetMonitorStats() MonitorStats
    // Logic:
    //   snap := m.stats.Snapshot()
    //   frameStat := snap["frame"]
    //   return MonitorStats{
    //       FPS:           float64(frameStat.Count),
    //       FramesLastSec: frameStat.Count,
    //   }

    // GetFPS returns the current FPS as float64.
    func (m *MonitorModule) GetFPS() float64
    // Logic: return m.GetMonitorStats().FPS

    // GetFrameCount returns the frame count from the last completed window.
    func (m *MonitorModule) GetFrameCount() int64
    // Logic: return m.GetMonitorStats().FramesLastSec
    ```

*   **Logic — `Start` の変更**:
    1. 既存の `vram_update` と `system` の Subscribe はそのまま
    2. **NEW**: `monCh, err := b.Subscribe("monitor")` を追加
    3. goroutine 起動: `m.receiveLoop(ctx, ch, sysCh, monCh)`（引数追加）

*   **Logic — `receiveLoop` の変更**:

    シグネチャ変更:
    ```go
    func (m *MonitorModule) receiveLoop(
        ctx context.Context,
        ch <-chan *bus.BusMessage,
        sysCh <-chan *bus.BusMessage,
        monCh <-chan *bus.BusMessage,  // NEW
    )
    ```

    select ブロックに以下を追加:
    ```go
    case msg := <-monCh:
        if msg != nil && msg.Target == "get_stats" && msg.Operation == bus.OpCommand {
            m.publishMonitorStats()
        }
    ```

    既存の `case msg := <-ch:` ハンドラの**先頭**に FPS カウントを追加:
    ```go
    case msg := <-ch:
        m.RecordFrame()  // NEW: count each received message as a frame tick
        // ... existing palette/vram_updated handling ...
    ```

*   **Logic — `onPaint` の変更**:

    関数の**先頭**に追加:
    ```go
    func (m *MonitorModule) onPaint(glctx gl.Context) {
        m.RecordFrame()  // NEW: count each paint as a frame
        // ... existing paint code ...
    }
    ```

*   **Logic — `publishMonitorStats`**:
    1. `ms := m.GetMonitorStats()`
    2. `data, _ := json.Marshal(ms)`
    3. `m.bus.Publish("monitor_update", &bus.BusMessage{Target: "stats_data", Operation: bus.OpCommand, Data: data, Source: m.Name()})`

### internal/statsserver (新規パッケージ)

#### [NEW] [features/neurom/internal/statsserver/server_test.go](file://features/neurom/internal/statsserver/server_test.go)

*   **Description**: HTTP スタットサーバーの単体テスト。モックプロバイダを使用。
*   **Test Cases**:

    | Test Name | Purpose |
    | :--- | :--- |
    | `TestHandleStats` | `GET /stats` でVRAM + Monitor 統合JSONが返ることを検証 |
    | `TestHandleStatsVRAM` | `GET /stats/vram` でVRAMのみのJSONが返ることを検証 |
    | `TestHandleStatsMonitor` | `GET /stats/monitor` でMonitorのみのJSONが返ることを検証 |
    | `TestHandleStatsContentType` | レスポンスの Content-Type が `application/json` であることを検証 |
    | `TestHandleStatsEmptyData` | コマンド実行なし状態でも正常なJSONが返ることを検証 |

*   **Technical Design**:
    ```go
    // モックプロバイダ
    type mockVRAMStats struct {
        data map[string]stats.CommandStat
    }
    func (m *mockVRAMStats) GetStats() map[string]stats.CommandStat { return m.data }

    type mockMonitorStats struct {
        fps float64
        fc  int64
    }
    func (m *mockMonitorStats) GetFPS() float64       { return m.fps }
    func (m *mockMonitorStats) GetFrameCount() int64   { return m.fc }

    func TestHandleStats(t *testing.T) {
        // httptest.NewServer でテスト
        // モックに固定値をセット
        // GET /stats → JSON パース → vram.commands と monitor.fps を検証
    }
    ```

#### [NEW] [features/neurom/internal/statsserver/server.go](file://features/neurom/internal/statsserver/server.go)

*   **Description**: VRAM / Monitor のスタットを HTTP で公開するサーバー。
*   **Technical Design**:

    ```go
    package statsserver

    import (
        "context"
        "encoding/json"
        "net/http"

        "github.com/axsh/neurom/internal/stats"
    )

    // VRAMStatsProvider provides VRAM performance statistics.
    type VRAMStatsProvider interface {
        GetStats() map[string]stats.CommandStat
    }

    // MonitorStatsProvider provides Monitor performance statistics.
    // Primitive return types avoid cross-package type dependency.
    type MonitorStatsProvider interface {
        GetFPS() float64
        GetFrameCount() int64
    }

    // StatsServer serves performance stats over HTTP.
    type StatsServer struct {
        vram    VRAMStatsProvider
        monitor MonitorStatsProvider
        server  *http.Server
    }

    func New(vram VRAMStatsProvider, monitor MonitorStatsProvider, port string) *StatsServer
    func (s *StatsServer) Start() error
    func (s *StatsServer) Shutdown(ctx context.Context) error
    ```

*   **Logic — `New`**:
    1. `mux := http.NewServeMux()`
    2. `mux.HandleFunc("GET /stats", s.handleAll)`
    3. `mux.HandleFunc("GET /stats/vram", s.handleVRAM)`
    4. `mux.HandleFunc("GET /stats/monitor", s.handleMonitor)`
    5. `s.server = &http.Server{Addr: ":" + port, Handler: mux}`

*   **Logic — `handleAll`**:
    ```go
    type monitorResponse struct {
        FPS           float64 `json:"fps"`
        FramesLastSec int64   `json:"frames_last_sec"`
    }
    type allResponse struct {
        VRAM    vramResponse    `json:"vram"`
        Monitor monitorResponse `json:"monitor"`
    }
    type vramResponse struct {
        Commands map[string]stats.CommandStat `json:"commands"`
    }
    ```
    1. `vramData := s.vram.GetStats()`
    2. `monResp := monitorResponse{FPS: s.monitor.GetFPS(), FramesLastSec: s.monitor.GetFrameCount()}`
    3. `resp := allResponse{VRAM: vramResponse{Commands: vramData}, Monitor: monResp}`
    4. `w.Header().Set("Content-Type", "application/json")`
    5. `json.NewEncoder(w).Encode(resp)`

*   **Logic — `handleVRAM`**: `vramResponse{Commands: s.vram.GetStats()}` を JSON レスポンス
*   **Logic — `handleMonitor`**: `monitorResponse{FPS: s.monitor.GetFPS(), FramesLastSec: s.monitor.GetFrameCount()}` を JSON レスポンス
*   **Logic — `Start`**: `go s.server.ListenAndServe()` (バックグラウンド起動)
*   **Logic — `Shutdown`**: `s.server.Shutdown(ctx)` でgraceful停止

### cmd

#### [MODIFY] [features/neurom/cmd/main.go](file://features/neurom/cmd/main.go)

*   **Description**: `--stats-port` フラグ追加、StatsServer の初期化・シャットダウン。
*   **Technical Design**:

    ```go
    import "github.com/axsh/neurom/internal/statsserver"

    func main() {
        // ... existing flags ...
        statsPort := flag.String("stats-port", "", "HTTP port for stats endpoint (disabled if empty)")
        flag.Parse()

        // ... existing module setup ...

        // Stats server (optional)
        if *statsPort != "" {
            ss := statsserver.New(vramMod, mon, *statsPort)
            if err := ss.Start(); err != nil {
                log.Printf("Stats server failed to start: %v", err)
            } else {
                defer func() {
                    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                    defer cancel()
                    ss.Shutdown(ctx)
                }()
            }
        }

        // ... rest of main ...
    }
    ```

*   **Logic**:
    1. `flag.String("stats-port", "", ...)` を既存フラグ群の後に追加
    2. `*statsPort != ""` の場合にのみ `statsserver.New()` → `Start()`
    3. `defer` で `Shutdown(ctx)` を登録（5秒タイムアウト付き）
    4. `statsserver.New` に `vramMod` と `mon` を渡す（両方とも `VRAMStatsProvider` / `MonitorStatsProvider` インターフェースを満たす）

### features/stats

#### [MODIFY] [features/stats/main.go](file://features/stats/main.go)

*   **Description**: HTTP エンドポイントからスタットを取得し、テーブル形式で表示する CLI。
*   **Technical Design**:

    ```go
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

    // JSON response structures (server response deserialization)
    type statsResponse struct {
        VRAM    vramStats    `json:"vram"`
        Monitor monitorStats `json:"monitor"`
    }
    type vramStats struct {
        Commands map[string]commandStat `json:"commands"`
    }
    type commandStat struct {
        Count int64 `json:"count"`
        AvgNs int64 `json:"avg_ns"`
        MaxNs int64 `json:"max_ns"`
    }
    type monitorStats struct {
        FPS           float64 `json:"fps"`
        FramesLastSec int64   `json:"frames_last_sec"`
    }

    func main()
    func fetchStats(endpoint string) (*statsResponse, error)
    func printStats(s *statsResponse)
    ```

*   **Logic — `main`**:
    1. `flag.String("endpoint", "http://localhost:8080/stats", ...)` でエンドポイント指定
    2. `flag.Bool("watch", false, ...)` で繰り返し取得モード
    3. ループ: `fetchStats(endpoint)` → `printStats(resp)`
    4. `--watch` でなければ 1回で終了
    5. `--watch` の場合、`fmt.Print("\033[H\033[2J")` でターミナルクリア → `time.Sleep(1 * time.Second)` → ループ先頭へ

*   **Logic — `fetchStats`**:
    1. `http.Get(endpoint)` でリクエスト
    2. レスポンスの Status が 200 でなければエラー
    3. `json.NewDecoder(resp.Body).Decode(&statsResponse{})` でパース
    4. パース結果を返す

*   **Logic — `printStats`**:
    1. `fmt.Println("=== VRAM Stats (1s avg) ===")`
    2. `fmt.Printf("%-24s %8s %10s %10s\n", "Command", "Count", "Avg(μs)", "Max(μs)")`
    3. 区切り線を出力
    4. `commands` のキーをソートして反復:
        *   `avgUs := float64(stat.AvgNs) / 1000.0`
        *   `maxUs := float64(stat.MaxNs) / 1000.0`
        *   `fmt.Printf("%-24s %8d %10.2f %10.2f\n", name, stat.Count, avgUs, maxUs)`
    5. 空行
    6. `fmt.Println("=== Monitor Stats ===")`
    7. `fmt.Printf("FPS: %.1f\n", s.Monitor.FPS)`

#### [MODIFY] [features/stats/go.mod](file://features/stats/go.mod)

*   **Description**: モジュール名修正（`nurom` → `neurom`、既存の typo 修正）。外部依存は不要（stdlib のみ）。
*   **変更内容**:
    ```
    module github.com/axsh/neurom/stats

    go 1.25.0
    ```

### integration

#### [NEW] [features/neurom/integration/stats_http_test.go](file://features/neurom/integration/stats_http_test.go)

*   **Description**: HTTP スタットエンドポイントの統合テスト。
*   **Test Cases**:

    | Test Name | Purpose |
    | :--- | :--- |
    | `TestHTTPStatsIntegration` | VRAM + Monitor を起動、VRAMコマンドを送信、HTTP `/stats` から JSON を取得し、VRAM commands と Monitor FPS が含まれることを検証 |
    | `TestHTTPStatsVRAMOnly` | HTTP `/stats/vram` で VRAM stats のみが返ることを検証 |
    | `TestHTTPStatsMonitorOnly` | HTTP `/stats/monitor` で Monitor stats のみが返ることを検証 |

*   **Technical Design**:
    ```go
    func TestHTTPStatsIntegration(t *testing.T) {
        // 1. ChannelBus + Manager + VRAM + Monitor(headless) を起動
        // 2. statsserver.New(vramMod, mon, "0") で起動 (port "0" = OS自動割り当て)
        //    → server.server.Addr でポート取得
        //    ※ テストではhttptest.NewServer使用が望ましい
        // 3. blit_rect を 5 回、draw_pixel を 10 回送信
        // 4. 1.2秒待機
        // 5. http.Get("http://localhost:<port>/stats")
        // 6. JSON パース
        // 7. resp.VRAM.Commands["blit_rect"].Count == 5 を検証
        // 8. resp.VRAM.Commands["draw_pixel"].Count == 10 を検証
        // 9. resp.Monitor.FPS >= 0 を検証 (headless なのでメッセージ数ベース)
        // 10. クリーンアップ
    }
    ```

## Step-by-Step Implementation Guide

- [x] 1. **Monitor テスト追加**: `monitor_test.go` に `TestMonitorFPSCounter`, `TestMonitorGetStatsCommand`, `TestMonitorHeadlessMessageCount` を追加。
- [x] 2. **Monitor 実装変更**: `monitor.go` に `stats` フィールド追加、`RecordFrame()`, `GetMonitorStats()`, `GetFPS()`, `GetFrameCount()` を実装。`Start` に `monitor` トピック Subscribe 追加。`receiveLoop` にシグネチャ変更 + `monCh` ケース追加 + FPS カウント追加。`onPaint` に `RecordFrame()` 追加。`publishMonitorStats()` 実装。
- [x] 3. **ビルド確認**: Monitor テスト全4テストパス。
- [x] 4. **StatsServer テスト作成**: `internal/statsserver/server_test.go` を作成。モックプロバイダで `TestHandleStats`, `TestHandleStatsVRAM`, `TestHandleStatsMonitor`, `TestHandleStatsContentType`, `TestHandleStatsEmptyData` を実装。
- [x] 5. **StatsServer 実装**: `internal/statsserver/server.go` を作成。`VRAMStatsProvider`, `MonitorStatsProvider` インターフェース、`StatsServer` 構造体、`New`, `Start`, `Shutdown`, `Addr`, 各ハンドラを実装。`net.Listener` 対応でポート0（テスト用）をサポート。
- [x] 6. **ビルド確認**: statsserver テスト全5テストパス。
- [x] 7. **main.go 変更**: `--stats-port` フラグ追加、`statsserver.New()` / `Start()` / `Shutdown()` の呼び出しを追加。`vramMod` を変数化。
- [x] 8. **Stats CLI 実装**: `features/stats/main.go` に `fetchStats`, `printStats` を実装。`go.mod` のモジュール名修正。
- [x] 9. **ビルド確認**: 本計画のテスト全パス。stats CLI ビルド確認。
- [x] 10. **統合テスト作成**: `integration/stats_http_test.go` を作成。`TestHTTPStatsIntegration`, `TestHTTPStatsVRAMOnly`, `TestHTTPStatsMonitorOnly` を実装。`setupHTTPTestEnv` ヘルパー関数で環境セットアップを共通化。
- [x] 11. **最終ビルド確認**: `./scripts/process/build.sh` 実行。本計画のテストは全パス。pre-existing の未実装機能テスト（monitor_sync, vram_enhancement, vram_page 等）は引き続き失敗中（本計画範囲外）。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestHTTPStats"
    ```
    *   **Log Verification**: HTTP レスポンスが 200 OK で、JSON に `vram.commands` と `monitor.fps` が含まれること。

## Documentation

#### [MODIFY] [features/README.md](file://features/README.md)
*   **更新内容**: `features/stats` セクションを追加。Stats CLI の概要、使用方法 (`--endpoint`, `--watch`)、neurom の `--stats-port` フラグとの連携を記載。
