# 017-StatsCollectorFix-Part1

> **Source Specification**: prompts/phases/000-foundation/ideas/main/017-StatsCollectorFix.md

## Goal Description

`stats.Collector` をリングバッファベースに再設計し、Snapshot() の副作用除去、コマンドエントリの安定保持、固定ウィンドウ境界、1s/10s/30s の複数ウィンドウ統計を実現する。あわせて statsserver のレスポンス構造を更新し、モニタのフレーム二重カウントを修正する。

## User Review Required

- `CommandStat` の JSON 構造が変わるため、バス経由の `stats_data` publish のペイロード形式も変わる。外部にこのデータを消費するコンポーネントが今後追加された場合、影響を受ける。現時点では消費者がいないため問題なしと判断。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 要件1: Snapshot() の副作用除去 | Proposed Changes > stats > stats.go — Snapshot() |
| 要件2: コマンドエントリの安定表示 | Proposed Changes > stats > stats.go — knownCmds + Snapshot() |
| 要件3: ウィンドウ境界の固定化 | Proposed Changes > stats > stats.go — advance() + wall-clock truncation |
| 要件4: 複数ウィンドウ期間の統計 | Proposed Changes > stats > stats.go — ring buffer + aggregate |
| 要件5: モニタのフレームカウント整理 | Proposed Changes > monitor > monitor.go — receiveLoop 修正 |

## Proposed Changes

### stats パッケージ (`features/neurom/internal/stats/`)

#### [MODIFY] [stats_test.go](file://features/neurom/internal/stats/stats_test.go)

*   **Description**: 新しいリングバッファベースの Collector に対応するテストに全面書き換え。
*   **Test Cases**:

| テスト名 | 検証内容 |
| :--- | :--- |
| `TestSnapshot_NoSideEffect` | Snapshot() を2回連続で呼び、同一の結果が返ること。Snapshot() だけではウィンドウが回転しないこと（Record なしで時間を進めた場合、Snapshot が最後の Record 時点のデータを返し続けること） |
| `TestCommandEntry_Persists` | ウィンドウ1で cmd_a, cmd_b を記録 → ウィンドウ2で cmd_a のみ記録 → Snapshot に cmd_b が `Last1s.Count: 0` で残ること |
| `TestWindowBoundary_Fixed` | nowFunc で T=0.5s に Record → T=1.5s に Record → 最初の Record がスロット second=0 に入り、2番目が second=1 に入ること（壁時計の秒境界に揃っていること） |
| `TestMultiWindow_1s_10s_30s` | 3つの異なるウィンドウ(second)に Record → Snapshot で Last1s は直近1スロット、Last10s は合算、Last30s は全合算となること |
| `TestMultiWindow_Rollover` | 31 個以上のウィンドウを順次記録 → 最初のウィンドウのデータが Last30s から消えること |
| `TestCollectorConcurrency` | 複数 goroutine から Record + Snapshot を並行実行してパニックやレースが発生しないこと |
| `TestSnapshotEmpty` | 新規 Collector の Snapshot が空マップを返すこと |
| `TestAdvanceLargeGap` | 40秒以上のギャップがある場合に advance が正しく全スロットをクリアすること |
| `TestAggregateAvgAndMax` | 複数スロットの AvgNs が加重平均、MaxNs が最大値であること |

#### [MODIFY] [stats.go](file://features/neurom/internal/stats/stats.go)

*   **Description**: リングバッファベースの Collector に全面書き換え。
*   **Technical Design**:

    **定数**:
    ```go
    const SlotCount = 30
    ```

    **型定義**:
    ```go
    type WindowStats struct {
        Count int64 `json:"count"`
        AvgNs int64 `json:"avg_ns"`
        MaxNs int64 `json:"max_ns"`
    }

    type CommandStat struct {
        Last1s  WindowStats `json:"last_1s"`
        Last10s WindowStats `json:"last_10s"`
        Last30s WindowStats `json:"last_30s"`
    }

    type windowAccum struct {
        count   int64
        totalNs int64
        maxNs   int64
    }

    type slotData struct {
        second int64
        accum  map[string]*windowAccum
    }

    type Collector struct {
        mu        sync.Mutex
        slots     [SlotCount]slotData
        headIdx   int
        knownCmds map[string]struct{}
        nowFunc   func() time.Time
    }

    // aggregator は Snapshot 内で複数スロットを集約するための内部ヘルパー
    type aggregator struct {
        count   int64
        totalNs int64
        maxNs   int64
    }
    ```

*   **Logic**:

    **`NewCollector()`**:
    - `nowFunc = time.Now`
    - `knownCmds` を空 map で初期化
    - `slots[0]` を `{second: now.Truncate(Second).Unix(), accum: empty map}` で初期化
    - `headIdx = 0`

    **`SetNowFunc(fn)`**: （テスト用）既存と同様

    **`Record(command, d)`**:
    1. ロック取得
    2. `sec = nowFunc().Truncate(Second).Unix()`
    3. `sec > slots[headIdx].second` なら `advance(sec)` を呼ぶ（ウィンドウ前進）
    4. `knownCmds[command]` を登録
    5. `slots[headIdx].accum[command]` のアキュムレータに count++, totalNs+=ns, maxNs 更新

    **`advance(sec)`**:（非公開メソッド、ロック取得済みの前提）
    1. `gap = sec - slots[headIdx].second`
    2. `steps = min(gap, SlotCount)` — 30 を超える場合は全スロットクリアと等価
    3. `i = 1..steps` でループ:
       - `idx = (headIdx + i) % SlotCount`
       - `slots[idx] = slotData{second: sec - steps + i, accum: empty map}`
    4. `headIdx = (headIdx + steps) % SlotCount`

    **`Snapshot()`**: — **副作用なし（rotate しない）**
    1. ロック取得
    2. `currentSec = nowFunc().Truncate(Second).Unix()`
    3. `knownCmds` の全コマンドについてループ:
       - 3つの `aggregator`（s1, s10, s30）を初期化
       - 全 30 スロットをスキャン:
         - `age = currentSec - slot.second`（0 = 書き込み中、1 = 直近完了、...）
         - `age < 1 || age > 30` → スキップ（書き込み中スロットと期限切れを除外）
         - `slot.accum[cmd]` が nil → スキップ
         - `age == 1` → s1 に加算
         - `age <= 10` → s10 に加算
         - `age <= 30` → s30 に加算
       - `CommandStat{Last1s: s1.toWindowStats(), Last10s: s10.toWindowStats(), Last30s: s30.toWindowStats()}`
    4. 結果マップを返す

    **`aggregator.add(acc)`**:
    - `count += acc.count`, `totalNs += acc.totalNs`, `maxNs = max(maxNs, acc.maxNs)`

    **`aggregator.toWindowStats()`**:
    - `avg = totalNs / count`（count > 0 の場合）、count == 0 なら avg = 0
    - `WindowStats{Count: count, AvgNs: avg, MaxNs: maxNs}`

### statsserver パッケージ (`features/neurom/internal/statsserver/`)

#### [MODIFY] [server_test.go](file://features/neurom/internal/statsserver/server_test.go)

*   **Description**: 新しいレスポンス構造に対応するようにテストを更新。
*   **Changes**:
    - `mockVRAMStats` — `GetStats()` の戻り値を新 `CommandStat` 形式に更新
    - `mockMonitorStats` — `MonitorStatsProvider` インタフェースの変更に合わせて `GetMonitorStats()` に変更（`stats.MonitorStats` を返す）
    - 全テストケースのアサーションを `.Last1s.Count` 等の新パスに更新
    - モニターレスポンスのアサーションを `FPS1s`, `FPS10s`, `FPS30s` に更新

#### [MODIFY] [server.go](file://features/neurom/internal/statsserver/server.go)

*   **Description**: レスポンス構造とプロバイダインタフェースを更新。
*   **Technical Design**:

    **MonitorStatsProvider インタフェースの変更**:
    ```go
    // 変更前:
    // type MonitorStatsProvider interface {
    //     GetFPS() float64
    //     GetFrameCount() int64
    // }

    // 変更後:
    type MonitorStatsProvider interface {
        GetMonitorStats() stats.MonitorStats
    }
    ```

    **stats パッケージに追加する型**（stats.go に定義）:
    ```go
    type MonitorStats struct {
        FPS1s  float64 `json:"fps_1s"`
        FPS10s float64 `json:"fps_10s"`
        FPS30s float64 `json:"fps_30s"`
    }
    ```

    **レスポンス構造の変更**:
    ```go
    // monitorResponse は MonitorStats をそのまま埋め込む
    type monitorResponse = stats.MonitorStats

    // allResponse の Monitor フィールドも MonitorStats に
    type allResponse struct {
        VRAM    vramResponse    `json:"vram"`
        Monitor stats.MonitorStats `json:"monitor"`
    }
    ```

*   **Logic**:
    - `handleAll`: `s.monitor.GetMonitorStats()` を直接 `Monitor` フィールドに代入
    - `handleVRAM`: 変更なし（CommandStat の型が変わるだけで、JSON シリアライズは自動対応）
    - `handleMonitor`: `s.monitor.GetMonitorStats()` の結果をそのまま JSON エンコード

    VRAMStatsProvider インタフェースは変更なし（`GetStats() map[string]stats.CommandStat` のまま）。

### monitor パッケージ (`features/neurom/internal/modules/monitor/`)

#### [MODIFY] [monitor_test.go](file://features/neurom/internal/modules/monitor/monitor_test.go)

*   **Description**: 新 CommandStat 構造への対応と、RecordFrame 単一ソース化の検証。
*   **Changes**:
    - `TestMonitorFPSCounter`: `RecordFrame()` を60回呼び → 時間を進めて新ウィンドウへ → `GetMonitorStats()` の戻り値が `FPS1s: 60.0` であること、`FPS10s` と `FPS30s` が `60/10` と `60/30` であること（1ウィンドウ分のデータしかないため）
    - `TestMonitorHeadlessMessageCount`: ヘッドレスモードでは `onPaint` が呼ばれないため、vram_update メッセージを送っても FPS は計上されない。期待値を `FPS1s: 0` に変更
    - `TestMonitorGetStatsCommand`: publishMonitorStats のレスポンス JSON 構造を新形式に合わせてアサーション更新

#### [MODIFY] [monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: receiveLoop からの RecordFrame 除去、GetMonitorStats の更新。
*   **Technical Design**:

    **MonitorStats 構造体の変更**:
    ```go
    type MonitorStats struct {
        FPS1s  float64 `json:"fps_1s"`
        FPS10s float64 `json:"fps_10s"`
        FPS30s float64 `json:"fps_30s"`
    }
    ```

*   **Logic**:
    - `receiveLoop` の `case msg := <-ch:` ブロックから `m.RecordFrame()` を**削除**。`handleVRAMEvent(msg)` のみ残す
    - `onPaint` 内の `m.RecordFrame()` はそのまま維持
    - `GetMonitorStats()`:
      ```
      snap = m.stats.Snapshot()
      frameStat = snap["frame"]
      FPS1s  = float64(frameStat.Last1s.Count)
      FPS10s = float64(frameStat.Last10s.Count) / 10.0
      FPS30s = float64(frameStat.Last30s.Count) / 30.0
      ```
    - `GetFPS()` と `GetFrameCount()` を削除し、`GetMonitorStats()` に統一

### vram パッケージ (`features/neurom/internal/modules/vram/`)

#### [MODIFY] [vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: `publishStats` のペイロード形式が自動的に新 CommandStat に対応する。明示的な変更は不要（`v.stats.Snapshot()` の戻り値型が変わるだけ）。
*   **Logic**:
    - `GetStats()` — 変更なし。`v.stats.Snapshot()` を返すだけで、戻り値の型は `map[string]stats.CommandStat`（CommandStat の内部構造が変わる）
    - `publishStats()` — 変更なし。`json.Marshal` で新構造が自動シリアライズされる

## Step-by-Step Implementation Guide

### ステップ 1: stats パッケージのテスト作成

- [x] `stats_test.go` を全面書き換え
  - 新しい型（`WindowStats`, `CommandStat`）のインポート確認
  - `TestSnapshot_NoSideEffect`: nowFunc 制御で Record 後に時間を進め、Snapshot を2回呼んで同じ結果を確認
  - `TestCommandEntry_Persists`: ウィンドウ跨ぎでコマンド消失しないことを確認
  - `TestWindowBoundary_Fixed`: `Truncate(Second)` 境界の検証
  - `TestMultiWindow_1s_10s_30s`: 異なるスロットに記録して各ウィンドウの集約を検証
  - `TestMultiWindow_Rollover`: 31+ スロットでの古いデータ消失を検証
  - `TestCollectorConcurrency`: 並行アクセスの安全性確認
  - `TestSnapshotEmpty`: 空の Collector
  - `TestAdvanceLargeGap`: 大きな時間ギャップ
  - `TestAggregateAvgAndMax`: 加重平均と最大値の正確性

### ステップ 2: stats パッケージの実装

- [x] `stats.go` を全面書き換え
  - `WindowStats`, `CommandStat`, `MonitorStats` 型を定義
  - `slotData`, `windowAccum`, `aggregator` 内部型を定義
  - `Collector` 構造体をリングバッファベースに変更
  - `NewCollector()`, `SetNowFunc()`, `Record()`, `advance()`, `Snapshot()` を実装

### ステップ 3: ビルド確認（stats パッケージ）

- [x] `scripts/process/build.sh` を実行
  - stats パッケージの単体テストが全て PASS することを確認
  - コンパイルエラーがある場合（statsserver, monitor, vram が新型を使用するため）は一時的に許容し、次ステップで修正

### ステップ 4: statsserver のテスト更新

- [x] `server_test.go` を更新
  - `mockMonitorStats` を `GetMonitorStats() stats.MonitorStats` に変更
  - 全テストのアサーションを新レスポンス構造に合わせて更新

### ステップ 5: statsserver の実装更新

- [x] `server.go` を更新
  - `MonitorStatsProvider` インタフェースを `GetMonitorStats() stats.MonitorStats` に変更
  - `monitorResponse` を `stats.MonitorStats` に変更
  - ハンドラメソッドを更新

### ステップ 6: monitor のテスト更新

- [x] `monitor_test.go` を更新
  - `TestMonitorFPSCounter`: 新 `MonitorStats` 構造に合わせたアサーション
  - `TestMonitorHeadlessMessageCount`: 期待値を `0` に変更
  - `TestMonitorGetStatsCommand`: publishMonitorStats のレスポンス構造を更新

### ステップ 7: monitor の実装修正

- [x] `monitor.go` を修正
  - `MonitorStats` 構造体を FPS1s/10s/30s に変更
  - `receiveLoop` から `m.RecordFrame()` を削除
  - `GetMonitorStats()` のロジックを更新
  - `GetFPS()`, `GetFrameCount()` を削除

### ステップ 8: 全体ビルド確認

- [x] `scripts/process/build.sh` を実行
  - neurom モジュール全体のコンパイルと単体テストが PASS することを確認
  - 注: 統合テストパッケージで ZMQ ライブラリの既存パニックにより exit code=1 だが、全テスト自体は PASS

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    - `stats` パッケージ: 9 テストケース全 PASS
    - `statsserver` パッケージ: 既存テスト全 PASS（新構造対応済み）
    - `monitor` パッケージ: 既存テスト全 PASS（新構造対応済み）

## Documentation

本パートではドキュメント更新は不要。Part2 で統合テストと CLI 更新後にまとめて更新する。

## 継続計画について

本計画は Part2 に続く。Part2 では以下を実施する:
- 統合テスト（`stats_http_test.go`, `vram_stats_test.go`）の更新
- stats CLI（`features/stats/cmd/main.go`）の表示更新
- ドキュメント更新
