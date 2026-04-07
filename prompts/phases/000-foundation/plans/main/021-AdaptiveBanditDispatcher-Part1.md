# 021-AdaptiveBanditDispatcher-Part1

> **Source Specification**: prompts/phases/000-foundation/ideas/main/021-AdaptiveBanditDispatcher.md

## Goal Description

Stats Collector に EMA（指数移動平均）カラムを追加し、低頻度コマンドでも常に直近の性能推定値を表示可能にする。併せて、ディスパッチャの戦略名を整理し `dynamic` → `heuristic` へのリネームとエイリアス対応を行う。Part 2 で実装する `adaptiveDispatcher` の基盤を整える。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 要件1: 戦略名の整理 (static/heuristic/dynamic alias) | Proposed Changes > dispatcher.go (factory update) |
| 要件1: デフォルトを adaptive に変更 | Part 2 で実施（Part 1 ではデフォルトは `dynamic` のまま） |
| 要件7: 既存テストの互換性 | Proposed Changes > dispatcher_test.go |
| 要件9: Stats への EMA カラム追加 | Proposed Changes > stats.go |
| 要件10: Stats EMA の具体仕様 | Proposed Changes > stats.go (emaTracker, Record, Snapshot) |
| 要件11: Stats CLI の表示更新 | Proposed Changes > stats CLI main.go |
| 要件12: Stats HTTP レスポンスの更新 | Proposed Changes > stats.go (CommandStat に EmaNs 追加で JSON 自動反映) |

## Proposed Changes

### stats パッケージ

#### [MODIFY] [stats_test.go](file://features/neurom/internal/stats/stats_test.go)

*   **Description**: EMA 関連のテストを追加する。既存テストはすべて維持。
*   **Technical Design**:
    *   新規テスト関数を追加。既存の `SetNowFunc` パターンを踏襲。
*   **Logic**:

    **TestEMA_FirstRecord**: `Record()` 初回呼び出しで EMA が実行時間そのままの値になること
    ```go
    func TestEMA_FirstRecord(t *testing.T) {
        c := NewCollector()
        c.Record("cmd_a", 1*time.Millisecond)
        snap := c.Snapshot()
        if snap["cmd_a"].EmaNs != 1_000_000 {
            t.Errorf("EmaNs = %d, want 1000000", snap["cmd_a"].EmaNs)
        }
    }
    ```

    **TestEMA_DecayUpdate**: 2回目以降の `Record()` で EMA が `α=0.05` に従い更新されること
    ```go
    func TestEMA_DecayUpdate(t *testing.T) {
        c := NewCollector()
        c.Record("cmd_a", 1*time.Millisecond)   // EMA = 1_000_000
        c.Record("cmd_a", 3*time.Millisecond)   // EMA = 0.95 * 1_000_000 + 0.05 * 3_000_000 = 1_100_000
        snap := c.Snapshot()
        expected := int64(0.95*1_000_000 + 0.05*3_000_000)
        if snap["cmd_a"].EmaNs != expected {
            t.Errorf("EmaNs = %d, want %d", snap["cmd_a"].EmaNs, expected)
        }
    }
    ```

    **TestEMA_Persists_NoRecord**: `Record()` が呼ばれない間も EMA が保持されること
    ```go
    func TestEMA_Persists_NoRecord(t *testing.T) {
        c := NewCollector()
        base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
        now := base
        c.SetNowFunc(func() time.Time { return now })

        c.Record("cmd_a", 1*time.Millisecond)
        snap1 := c.Snapshot()
        emaBefore := snap1["cmd_a"].EmaNs

        now = base.Add(35 * time.Second)
        snap2 := c.Snapshot()
        // 30s window 外だが EMA は保持
        if snap2["cmd_a"].Last30s.Count != 0 {
            t.Errorf("Last30s.Count should be 0 after 35s, got %d", snap2["cmd_a"].Last30s.Count)
        }
        if snap2["cmd_a"].EmaNs != emaBefore {
            t.Errorf("EmaNs changed from %d to %d without Record", emaBefore, snap2["cmd_a"].EmaNs)
        }
    }
    ```

    **TestEMA_InSnapshot**: Snapshot 返り値に EmaNs が含まれ、未記録コマンドは 0 であること
    ```go
    func TestEMA_InSnapshot(t *testing.T) {
        c := NewCollector()
        c.Record("cmd_a", 500*time.Microsecond)
        c.Record("cmd_b", 0)
        snap := c.Snapshot()
        if snap["cmd_a"].EmaNs <= 0 {
            t.Errorf("cmd_a EmaNs should be > 0, got %d", snap["cmd_a"].EmaNs)
        }
    }
    ```

#### [MODIFY] [stats.go](file://features/neurom/internal/stats/stats.go)

*   **Description**: `emaTracker` 型を追加し、`Collector` に `cmdEMA` マップを追加。`Record()` で EMA 更新、`Snapshot()` で EMA 読み取りを実装。`CommandStat` に `EmaNs` フィールドを追加。
*   **Technical Design**:

    ```go
    const StatsEMAAlpha = 0.05

    type emaTracker struct {
        value  float64
        seeded bool
    }

    type CommandStat struct {
        Last1s  WindowStats `json:"last_1s"`
        Last10s WindowStats `json:"last_10s"`
        Last30s WindowStats `json:"last_30s"`
        EmaNs   int64       `json:"ema_ns"`
    }

    type Collector struct {
        mu        sync.Mutex
        slots     [SlotCount]slotData
        headIdx   int
        knownCmds map[string]struct{}
        nowFunc   func() time.Time
        cmdEMA    map[string]*emaTracker
    }
    ```

*   **Logic**:

    **NewCollector()** の変更:
    - `cmdEMA: make(map[string]*emaTracker)` を初期化に追加

    **SetNowFunc()** の変更:
    - `cmdEMA` のリセットは不要（EMA は時間リセットに影響されない）。ただし、テスト用に `cmdEMA` をクリアするかは、既存テストの互換性を見て判断。現在の SetNowFunc は slots をリセットするが、EMA は蓄積型なのでリセットしない。

    **Record()** への EMA 更新追加:
    ```go
    func (c *Collector) Record(command string, d time.Duration) {
        c.mu.Lock()
        defer c.mu.Unlock()

        // 既存: スロット回転 (advance)
        sec := c.nowFunc().Truncate(time.Second).Unix()
        if sec > c.slots[c.headIdx].second {
            c.advance(sec)
        }

        c.knownCmds[command] = struct{}{}

        // 既存: スロット内蓄積
        acc := c.slots[c.headIdx].accum[command]
        if acc == nil {
            acc = &windowAccum{}
            c.slots[c.headIdx].accum[command] = acc
        }
        acc.count++
        ns := d.Nanoseconds()
        acc.totalNs += ns
        if ns > acc.maxNs {
            acc.maxNs = ns
        }

        // 新規: EMA 更新
        tracker := c.cmdEMA[command]
        if tracker == nil {
            tracker = &emaTracker{}
            c.cmdEMA[command] = tracker
        }
        nsf := float64(ns)
        if !tracker.seeded {
            tracker.value = nsf
            tracker.seeded = true
        } else {
            tracker.value = (1-StatsEMAAlpha)*tracker.value + StatsEMAAlpha*nsf
        }
    }
    ```

    **Snapshot()** への EMA 読み取り追加:
    ```go
    func (c *Collector) Snapshot() map[string]CommandStat {
        c.mu.Lock()
        defer c.mu.Unlock()

        currentSec := c.nowFunc().Truncate(time.Second).Unix()
        result := make(map[string]CommandStat, len(c.knownCmds))

        for cmd := range c.knownCmds {
            var s1, s10, s30 aggregator
            // 既存: スロット走査ロジック（変更なし）
            for i := range SlotCount {
                slot := &c.slots[i]
                if slot.accum == nil { continue }
                age := currentSec - slot.second
                if age < 1 || age > 30 { continue }
                acc := slot.accum[cmd]
                if acc == nil { continue }
                if age == 1 { s1.add(acc) }
                if age <= 10 { s10.add(acc) }
                s30.add(acc)
            }

            // 新規: EMA 読み取り
            emaNs := int64(0)
            if t := c.cmdEMA[cmd]; t != nil && t.seeded {
                emaNs = int64(t.value)
            }

            result[cmd] = CommandStat{
                Last1s:  s1.toWindowStats(),
                Last10s: s10.toWindowStats(),
                Last30s: s30.toWindowStats(),
                EmaNs:   emaNs,
            }
        }
        return result
    }
    ```

### vram ディスパッチャ

#### [MODIFY] [dispatcher_test.go](file://features/neurom/internal/modules/vram/dispatcher_test.go)

*   **Description**: ファクトリ関数テストを新しい戦略名に対応させる。既存の `dynamicDispatcher` テスト 12 件はすべて維持。
*   **Logic**:

    **TestNewDispatchStrategy_Dynamic の変更**: `"dynamic"` が `*dynamicDispatcher` を返すことを検証（エイリアス）
    ```go
    func TestNewDispatchStrategy_Dynamic(t *testing.T) {
        s := newDispatchStrategy("dynamic", 4)
        if _, ok := s.(*dynamicDispatcher); !ok {
            t.Errorf("expected *dynamicDispatcher for 'dynamic' alias, got %T", s)
        }
    }
    ```

    **TestNewDispatchStrategy_Heuristic の新規追加**: `"heuristic"` が `*dynamicDispatcher` を返すことを検証
    ```go
    func TestNewDispatchStrategy_Heuristic(t *testing.T) {
        s := newDispatchStrategy("heuristic", 4)
        if _, ok := s.(*dynamicDispatcher); !ok {
            t.Errorf("expected *dynamicDispatcher for 'heuristic', got %T", s)
        }
    }
    ```

    **TestNewDispatchStrategy_Default の変更**: 空文字列のデフォルトは Part 1 では `*dynamicDispatcher` のまま（Part 2 で `*adaptiveDispatcher` に変更）
    ```go
    func TestNewDispatchStrategy_Default(t *testing.T) {
        s := newDispatchStrategy("", 4)
        if _, ok := s.(*dynamicDispatcher); !ok {
            t.Errorf("expected *dynamicDispatcher for empty string, got %T", s)
        }
    }
    ```

#### [MODIFY] [dispatcher.go](file://features/neurom/internal/modules/vram/dispatcher.go)

*   **Description**: `newDispatchStrategy` ファクトリ関数を更新し、`"heuristic"` を `"dynamic"` のエイリアスとして受け付けるようにする。
*   **Logic**:

    ```go
    func newDispatchStrategy(strategy string, maxWorkers int) dispatchStrategy {
        switch strategy {
        case "static":
            return &staticDispatcher{maxWorkers: maxWorkers}
        case "heuristic", "dynamic", "":
            return newDynamicDispatcher(maxWorkers)
        default:
            return newDynamicDispatcher(maxWorkers)
        }
    }
    ```

    **注**: Part 1 ではデフォルト (`""`) は `dynamicDispatcher` のまま。Part 2 で `adaptiveDispatcher` 導入時にデフォルトを変更する。

### 統合テスト

#### [MODIFY] [stats_http_test.go](file://features/neurom/integration/stats_http_test.go)

*   **Description**: HTTP レスポンスの JSON に `ema_ns` フィールドが含まれることを検証するテストを追加。
*   **Logic**:

    **TestHTTPStats_EMAField の新規追加**:
    ```go
    func TestHTTPStats_EMAField(t *testing.T) {
        b, _, _, ss, _ := setupHTTPTestEnv(t)

        for range 10 {
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

        // JSON に ema_ns フィールドが存在することを確認
        var raw map[string]json.RawMessage
        json.NewDecoder(resp.Body).Decode(&raw)
        cmdRaw := raw["commands"]
        var cmds map[string]json.RawMessage
        json.Unmarshal(cmdRaw, &cmds)
        dpRaw := cmds["draw_pixel"]
        var dp map[string]json.RawMessage
        json.Unmarshal(dpRaw, &dp)
        if _, ok := dp["ema_ns"]; !ok {
            t.Error("draw_pixel missing ema_ns field in JSON response")
        }

        // EmaNs > 0 であることを確認
        var dpStat stats.CommandStat
        json.Unmarshal(dpRaw, &dpStat)
        if dpStat.EmaNs <= 0 {
            t.Errorf("draw_pixel EmaNs = %d, want > 0", dpStat.EmaNs)
        }
    }
    ```

    **TestHTTPStats_MultiWindow の更新**: `ema_ns` フィールドの存在確認を追加
    ```go
    // 既存ループ内に ema_ns チェックを追加:
    for _, key := range []string{"last_1s", "last_10s", "last_30s", "ema_ns"} {
        if _, ok := dp[key]; !ok {
            t.Errorf("draw_pixel missing field %s", key)
        }
    }
    ```

### Stats CLI

#### [MODIFY] [main.go (stats CLI)](file://features/stats/cmd/main.go)

*   **Description**: `commandStat` に `EmaNs` フィールドを追加し、表示に EMA カラムを追加する。
*   **Logic**:

    **commandStat 構造体の変更**:
    ```go
    type commandStat struct {
        Last1s  windowStats `json:"last_1s"`
        Last10s windowStats `json:"last_10s"`
        Last30s windowStats `json:"last_30s"`
        EmaNs   int64       `json:"ema_ns"`
    }
    ```

    **printStats 関数の変更**:
    ```go
    func printStats(s *statsResponse) {
        fmt.Println("=== VRAM Stats ===")
        fmt.Printf("%-24s ──── 1s ────   ──── 10s ────   ──── 30s ────   ── EMA ──\n", "Command")
        fmt.Printf("%-24s %5s %7s  %5s %7s   %5s %7s   %7s\n",
            "", "Count", "Avg(μs)", "Count", "Avg(μs)", "Count", "Avg(μs)", "Avg(μs)")
        fmt.Println("──────────────────────────────────────────────────────────────────────────────────")

        // ... sort logic unchanged ...

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

        // ... monitor stats unchanged ...
    }
    ```

## Step-by-Step Implementation Guide

### ステップ 1: Stats EMA テストの追加

- [x] `features/neurom/internal/stats/stats_test.go` に以下のテスト関数を追加:
  - `TestEMA_FirstRecord`
  - `TestEMA_DecayUpdate`
  - `TestEMA_Persists_NoRecord`
  - `TestEMA_InSnapshot`
- [x] ビルドを実行し、テストがコンパイルエラーになることを確認（`EmaNs` フィールド未定義のため）

### ステップ 2: Stats EMA の実装

- [x] `features/neurom/internal/stats/stats.go` に以下を追加:
  - `StatsEMAAlpha` 定数（`0.05`）
  - `emaTracker` 構造体
  - `CommandStat` に `EmaNs int64` フィールド追加
  - `Collector` に `cmdEMA map[string]*emaTracker` フィールド追加
  - `NewCollector()` で `cmdEMA` を初期化
  - `Record()` の末尾に EMA 更新ロジック追加
  - `Snapshot()` の `result[cmd]` 構築時に `EmaNs` を設定

### ステップ 3: Stats EMA テストの実行

- [x] `./scripts/process/build.sh` を実行
- [x] 既存テスト 8 件 + 新規テスト 4 件 = 12 件が全 PASS することを確認

### ステップ 4: ディスパッチャ戦略名テストの追加

- [x] `features/neurom/internal/modules/vram/dispatcher_test.go` に以下を追加:
  - `TestNewDispatchStrategy_Heuristic`
- [x] 既存の `TestNewDispatchStrategy_Dynamic` と `TestNewDispatchStrategy_Default` は変更不要（Part 1 ではデフォルト動作は変わらない）
- [x] ビルドを実行し、`TestNewDispatchStrategy_Heuristic` がコンパイルは通るが FAIL になることを確認

### ステップ 5: ディスパッチャ戦略名の実装

- [x] `features/neurom/internal/modules/vram/dispatcher.go` の `newDispatchStrategy` を更新:
  - `case "heuristic", "dynamic", "":` で `dynamicDispatcher` を返す

### ステップ 6: ディスパッチャテストの実行

- [x] `./scripts/process/build.sh` を実行
- [x] 既存テスト 12 件 + 新規テスト 1 件 = 13 件が全 PASS することを確認

### ステップ 7: 統合テストの追加

- [x] `features/neurom/integration/stats_http_test.go` に以下を追加:
  - `TestHTTPStats_EMAField`
- [x] 既存の `TestHTTPStats_MultiWindow` の JSON フィールドチェックに `"ema_ns"` を追加

### ステップ 8: 統合テストの実行

- [x] `./scripts/process/integration_test.sh` を実行
- [x] 新規テスト含め全件 PASS することを確認

### ステップ 9: Stats CLI の更新

- [x] `features/stats/cmd/main.go` の `commandStat` に `EmaNs` フィールドを追加
- [x] `printStats` のヘッダーと行フォーマットに EMA カラムを追加

### ステップ 10: 全体ビルドと検証

- [x] `./scripts/process/build.sh` を実行
- [x] `./scripts/process/integration_test.sh` を実行
- [x] 全テスト PASS を確認（ZMQ既知問題 020-AppMainShutdownHang を除く）

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **Log Verification**: `TestHTTPStats_EMAField` が PASS し、`ema_ns` フィールドが JSON レスポンスに含まれていること

## Documentation

#### [MODIFY] [README.md](file://features/README.md)

*   **更新内容**: stats CLI の出力例に EMA カラムを追加。`-vram-strategy` の説明に `heuristic` を追加。

## 継続計画について

Part 2 (`021-AdaptiveBanditDispatcher-Part2.md`) で以下を実施する:
- `adaptiveDispatcher` の実装（バンディットアルゴリズム本体）
- デフォルト戦略の `adaptive` への変更
- `main.go` の CLI フラグ更新
- パフォーマンス評価マトリックスの実行
