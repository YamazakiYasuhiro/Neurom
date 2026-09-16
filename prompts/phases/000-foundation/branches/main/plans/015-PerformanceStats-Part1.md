# 015-PerformanceStats-Part1

> **Source Specification**: `prompts/phases/000-foundation/ideas/main/015-PerformanceStats.md`

## Goal Description

共通パフォーマンス計測ユーティリティ (`internal/stats`) を新規作成し、VRAMモジュールの全コマンドに計測を組み込む。バスコマンド `get_stats` でスナップショットを取得可能にする。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: 共通計測ユーティリティ — CommandStat 構造体 | Proposed Changes > stats.go |
| R1: Collector 構造体 (current/snapshot/windowEnd) | Proposed Changes > stats.go |
| R1: Record メソッド — ウィンドウ回転ロジック | Proposed Changes > stats.go |
| R1: Snapshot メソッド | Proposed Changes > stats.go |
| R1: スレッドセーフ (sync.Mutex) | Proposed Changes > stats.go |
| R1: 将来拡張を考慮した設計 (nowFunc) | Proposed Changes > stats.go |
| R2: handleMessage 内での各コマンド計測 | Proposed Changes > vram.go |
| R2: 計測オーバーヘッドを time.Now() 2回程度に抑制 | Proposed Changes > vram.go (defer パターン) |
| R3: バス `get_stats` コマンド追加 | Proposed Changes > vram.go |
| R3: `stats_data` イベント発行 (JSON) | Proposed Changes > vram.go |
| R3: GetStats() エクスポートメソッド | Proposed Changes > vram.go |

## Proposed Changes

### internal/stats (新規パッケージ)

#### [NEW] [features/neurom/internal/stats/stats_test.go](file://features/neurom/internal/stats/stats_test.go)

*   **Description**: Collector の単体テスト。テーブル駆動 + 並行安全テスト。
*   **Technical Design**:
    *   `nowFunc` フィールドを差し替えて時刻を制御する
    *   テスト内で `mockNow` 変数を進めることでウィンドウ回転を再現
*   **Test Cases**:

    | Test Name | Purpose |
    | :--- | :--- |
    | `TestCollectorRecord` | 複数コマンドを Record し、ウィンドウ回転後に Snapshot で正しい Count/AvgNs/MaxNs が返ることを検証 |
    | `TestCollectorWindowRotation` | 時刻を1秒以上進め、新しいウィンドウで Record し、前ウィンドウが snapshot に確定されることを検証 |
    | `TestCollectorMaxNs` | 異なる Duration で Record し、MaxNs が最大値を保持することを検証 |
    | `TestCollectorEmptySnapshot` | Record なしで Snapshot を呼ぶと空 map が返ることを検証 |
    | `TestCollectorConcurrency` | 10 goroutine から並行 Record し、`-race` でデータ競合がないことを検証 |
    | `TestCollectorMultipleCommands` | 複数コマンド名を Record し、各コマンドが独立にカウントされることを検証 |

    ```go
    // テーブル駆動の骨格
    tests := []struct {
        name     string
        records  []struct{ cmd string; dur time.Duration }
        advance  time.Duration // mockNow を進める量
        wantSnap map[string]CommandStat
    }{...}
    ```

#### [NEW] [features/neurom/internal/stats/stats.go](file://features/neurom/internal/stats/stats.go)

*   **Description**: 汎用的な実行時間計測・蓄積ユーティリティ。
*   **Technical Design**:

    ```go
    package stats

    type CommandStat struct {
        Count int64 `json:"count"`
        AvgNs int64 `json:"avg_ns"`
        MaxNs int64 `json:"max_ns"`
    }

    // windowAccum accumulates timing data within a single 1-second window.
    type windowAccum struct {
        count   int64
        totalNs int64
        maxNs   int64
    }

    type Collector struct {
        mu        sync.Mutex
        current   map[string]*windowAccum
        snapshot  map[string]CommandStat
        windowEnd time.Time
        nowFunc   func() time.Time  // injectable for testing
    }

    func NewCollector() *Collector
    // Returns Collector with:
    //   current:   empty map
    //   snapshot:  empty map
    //   windowEnd: time.Now().Add(time.Second)
    //   nowFunc:   time.Now

    func (c *Collector) Record(command string, d time.Duration)
    func (c *Collector) Snapshot() map[string]CommandStat
    ```

*   **Logic — `Record`**:
    1. `c.mu.Lock()`
    2. `now := c.nowFunc()`
    3. `now` が `c.windowEnd` を超えている場合:
        *   `c.snapshot` を新しい map で作り直す
        *   `c.current` の各エントリについて `CommandStat` を計算:
            *   `Count = accum.count`
            *   `AvgNs = accum.totalNs / accum.count` (count > 0 の場合)
            *   `MaxNs = accum.maxNs`
        *   `c.current` を空 map でリセット
        *   `c.windowEnd = now.Add(time.Second)`
    4. `c.current[command]` が nil なら新規 `windowAccum` を作成
    5. `accum.count++`
    6. `accum.totalNs += d.Nanoseconds()`
    7. `d.Nanoseconds() > accum.maxNs` なら `accum.maxNs = d.Nanoseconds()`
    8. `c.mu.Unlock()`
*   **Logic — `Snapshot`**:
    1. `c.mu.Lock()`
    2. `c.snapshot` のディープコピーを作成
    3. `c.mu.Unlock()`
    4. コピーを返す

### internal/modules/vram

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: VRAMの stats 計測およびバスコマンドのテストを追加。
*   **Test Cases**:

    | Test Name | Purpose |
    | :--- | :--- |
    | `TestVRAMStatsRecording` | `draw_pixel`, `clear_vram`, `blit_rect` を各数回実行し、`GetStats()` の返却値で各コマンドの Count が記録されていることを検証。ウィンドウ回転のため、VRAMModule の `stats.nowFunc` を差し替えて1秒経過をシミュレート |
    | `TestVRAMGetStatsCommand` | `get_stats` コマンドを handleMessage に送信し、testBus の ch から `stats_data` ターゲットのメッセージを受信。Data を JSON パースし、`commands` キー配下にエントリが存在することを検証 |
    | `TestVRAMGetStatsNoLock` | `get_stats` は `mu.Lock()` を取得しないことを検証（同時に読み取りロックが取れることを確認） |

*   **Technical Design**:
    ```go
    func TestVRAMStatsRecording(t *testing.T) {
        b := &testBus{ch: make(chan *bus.BusMessage, 100)}
        v := New()
        v.bus = b

        // nowFunc を差し替え可能にするため、v.stats のフィールドを操作
        // draw_pixel を 5 回、clear_vram を 2 回 handleMessage で実行
        // nowFunc を 1秒以上進める
        // 何かを Record してウィンドウ回転をトリガー
        // v.GetStats() で Count を確認
    }
    ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: VRAMModule に stats.Collector を組み込み、全コマンドの計測 + `get_stats` コマンドを追加。
*   **Technical Design**:

    ```go
    import (
        // ... existing imports ...
        "encoding/json"
        "time"
        "github.com/axsh/neurom/internal/stats"
    )

    type VRAMModule struct {
        // ... existing fields ...
        stats *stats.Collector  // NEW
    }

    func New() *VRAMModule {
        return &VRAMModule{
            // ... existing initialization ...
            stats: stats.NewCollector(),  // NEW
        }
    }

    // GetStats returns the last completed window's per-command stats.
    func (v *VRAMModule) GetStats() map[string]stats.CommandStat

    // publishStats marshals the snapshot to JSON and publishes as stats_data.
    func (v *VRAMModule) publishStats()
    ```

*   **Logic — `handleMessage` の変更**:

    既存の `handleMessage` 冒頭に以下のロジックを挿入:

    1. `msg.Operation != OpCommand` なら return（既存と同じ）
    2. **NEW**: `msg.Target == "get_stats"` の場合 → `v.publishStats()` して return。**`mu.Lock()` を取得しない**（stats.Collector は自身の mutex で保護される）
    3. **NEW**: `start := time.Now()`
    4. **NEW**: `defer func() { v.stats.Record(msg.Target, time.Since(start)) }()`
    5. `v.mu.Lock()` / `defer v.mu.Unlock()`（既存と同じ）
    6. `switch msg.Target { ... }`（既存と同じ）

    計測対象コマンド（すべて自動的に計測される）:
    *   `mode`, `draw_pixel`, `set_palette`, `clear_vram`, `blit_rect`, `read_rect`, `copy_rect`, `set_palette_block`, `read_palette_block`, `blit_rect_transform`, `set_page_count`, `set_display_page`, `swap_pages`, `copy_page`, `set_page_size`

*   **Logic — `publishStats`**:
    1. `snap := v.stats.Snapshot()`
    2. `payload := map[string]interface{}{"commands": snap}`
    3. `data, _ := json.Marshal(payload)`
    4. `v.bus.Publish("vram_update", &bus.BusMessage{Target: "stats_data", Operation: bus.OpCommand, Data: data, Source: v.Name()})`

*   **Logic — `GetStats`**:
    1. `return v.stats.Snapshot()`

### integration

#### [NEW] [features/neurom/integration/vram_stats_test.go](file://features/neurom/integration/vram_stats_test.go)

*   **Description**: バス経由で VRAM stats を取得する統合テスト。
*   **Technical Design**:

    ```go
    func TestVRAMStatsIntegration(t *testing.T)
    // 1. ChannelBus + Manager で VRAM モジュールを起動
    // 2. blit_rect を 10 回送信
    // 3. draw_pixel を 20 回送信
    // 4. 1.2秒待機 (ウィンドウ確定)
    // 5. vram_update トピックを Subscribe
    // 6. vram トピックに get_stats コマンドを Publish
    // 7. stats_data メッセージを受信
    // 8. JSON パースして commands.blit_rect.count == 10,
    //    commands.draw_pixel.count == 20 を検証
    // 9. クリーンアップ (cancel + StopAll)
    ```

## Step-by-Step Implementation Guide

- [x] 1. **Stats テスト作成**: `internal/stats/stats_test.go` を作成。`TestCollectorRecord`, `TestCollectorWindowRotation`, `TestCollectorMaxNs`, `TestCollectorEmptySnapshot`, `TestCollectorConcurrency`, `TestCollectorMultipleCommands` を実装。
- [x] 2. **Stats 実装**: `internal/stats/stats.go` を作成。`CommandStat`, `windowAccum`, `Collector` 構造体、`NewCollector`, `Record`, `Snapshot`, `SetNowFunc` を実装。`Snapshot` にもウィンドウ回転ロジックを追加。
- [x] 3. **ビルド確認**: `./scripts/process/build.sh` を実行し stats パッケージのテストがパスすることを確認。
- [x] 4. **VRAM テスト追加**: `vram_test.go` に `TestVRAMStatsRecording`, `TestVRAMGetStatsCommand`, `TestVRAMGetStatsNoLock` を追加。
- [x] 5. **VRAM 実装変更**: `vram.go` に `stats` フィールド追加、`New()` で初期化、`handleMessage` に計測コード挿入、`get_stats` ハンドラ追加、`GetStats()` / `publishStats()` 実装。
- [x] 6. **ビルド確認**: `./scripts/process/build.sh` を実行し VRAM テストがパスすることを確認。
- [x] 7. **統合テスト作成**: `integration/vram_stats_test.go` を作成。`TestVRAMStatsIntegration` を実装。
- [x] 8. **最終ビルド確認**: `./scripts/process/build.sh` を実行。本計画のテストは全パス。pre-existing の未実装機能テスト（monitor_sync, vram_enhancement, vram_page 等）は引き続き失敗中（本計画範囲外）。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestVRAMStats"
    ```
    *   **Log Verification**: `stats_data` イベントが発行され、JSON に `commands` キーが含まれ、各コマンドの `count`, `avg_ns`, `max_ns` が 0 以上の整数であること。

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/015-PerformanceStats.md](file://prompts/phases/000-foundation/ideas/main/015-PerformanceStats.md)
*   **更新内容**: なし（仕様書は変更不要）

## 継続計画について

本計画は Part 1 です。Part 2 (`015-PerformanceStats-Part2.md`) で以下を実装します:

*   R4: Monitor FPS 計測
*   R5: Monitor スタット取得コマンド
*   R6: HTTP スタットエンドポイント
*   R7: Stats CLI (`features/stats`)
