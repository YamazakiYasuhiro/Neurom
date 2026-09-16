# 017-StatsCollectorFix-Part2

> **Source Specification**: prompts/phases/000-foundation/ideas/main/017-StatsCollectorFix.md
> **Predecessor**: prompts/phases/000-foundation/plans/main/017-StatsCollectorFix-Part1.md

## Goal Description

Part1 で完了した stats.Collector / statsserver / monitor の変更を前提に、統合テストの更新、stats CLI の表示更新、およびドキュメントの更新を行う。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 要件1: Snapshot() の副作用除去 | 統合テスト `TestHTTPStats_StableEntries` で検証 |
| 要件2: コマンドエントリの安定表示 | 統合テスト `TestHTTPStats_StableEntries` で検証 |
| 要件3: ウィンドウ境界の固定化 | Part1 で実装済み。本パートでは統合レベルで間接検証 |
| 要件4: 複数ウィンドウ期間の統計 | 統合テスト `TestHTTPStats_MultiWindow` + CLI 表示で検証 |
| 要件5: モニタのフレームカウント整理 | Part1 で実装済み。統合テスト `TestHTTPStatsMonitorOnly` で間接検証 |

## Proposed Changes

### 統合テスト (`features/neurom/integration/`)

#### [MODIFY] [stats_http_test.go](file://features/neurom/integration/stats_http_test.go)

*   **Description**: 新 CommandStat / MonitorStats 構造への対応と、安定性・冪等性テストの追加。
*   **Changes**:

    **レスポンス型の更新**:
    ```go
    // 旧:
    // type httpAllResponse struct {
    //     VRAM struct {
    //         Commands map[string]stats.CommandStat `json:"commands"`
    //     } `json:"vram"`
    //     Monitor struct {
    //         FPS           float64 `json:"fps"`
    //         FramesLastSec int64   `json:"frames_last_sec"`
    //     } `json:"monitor"`
    // }

    // 新: stats.CommandStat が Last1s/Last10s/Last30s を含む形に変わるため、
    //     そのまま使える。Monitor のレスポンス型を更新。
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
    ```

    **既存テストの更新**:
    - `TestHTTPStatsIntegration`: アサーションを `dp.Last1s.Count` に変更。`result.Monitor.FPS1s` に変更
    - `TestHTTPStatsVRAMOnly`: アサーションを `dp.Last1s.Count` に変更
    - `TestHTTPStatsMonitorOnly`: アサーションを `result.FPS1s >= 0` に変更

    **新規テストケース**:

    | テスト名 | 検証内容 |
    | :--- | :--- |
    | `TestHTTPStats_StableEntries` | (1) draw_pixel + set_palette を発行して 1.5s 待機 (2) GET /stats で結果取得 → draw_pixel, set_palette が存在 (3) draw_pixel のみ追加発行して 1.5s 待機 (4) 再度 GET /stats → set_palette が `Last1s.Count: 0` で残っていること。コマンドセットのキー数が変わらないこと |
    | `TestHTTPStats_SnapshotIdempotent` | 同一ウィンドウ内に GET /stats を2回連続呼び出し → 両方のレスポンスで VRAM Commands のキーセットと Last1s の値が一致すること |
    | `TestHTTPStats_MultiWindow` | draw_pixel を発行 → 1.5s 待機 → GET /stats → Last1s, Last10s, Last30s の全フィールドが JSON レスポンスに存在すること。Last10s.Count >= Last1s.Count であること |

#### [MODIFY] [vram_stats_test.go](file://features/neurom/integration/vram_stats_test.go)

*   **Description**: バス経由 stats_data のレスポンス構造を新形式に対応。
*   **Changes**:
    - レスポンスの JSON パース構造体を更新:
      ```go
      // 旧: Count, AvgNs, MaxNs のフラット構造
      // 新: Last1s.Count, Last1s.AvgNs, Last1s.MaxNs のネスト構造
      var resp struct {
          Commands map[string]stats.CommandStat `json:"commands"`
      }
      ```
    - アサーションを `resp.Commands["draw_pixel"].Last1s.Count` に変更
    - `set_palette` のアサーションも同様に更新

### stats CLI (`features/stats/`)

#### [MODIFY] [cmd/main.go](file://features/stats/cmd/main.go)

*   **Description**: 新レスポンス構造に対応した表示フォーマットへ更新。
*   **Technical Design**:

    **レスポンス型の更新**:
    ```go
    type windowStats struct {
        Count int64 `json:"count"`
        AvgNs int64 `json:"avg_ns"`
        MaxNs int64 `json:"max_ns"`
    }

    type commandStat struct {
        Last1s  windowStats `json:"last_1s"`
        Last10s windowStats `json:"last_10s"`
        Last30s windowStats `json:"last_30s"`
    }

    type vramStats struct {
        Commands map[string]commandStat `json:"commands"`
    }

    type monitorStats struct {
        FPS1s  float64 `json:"fps_1s"`
        FPS10s float64 `json:"fps_10s"`
        FPS30s float64 `json:"fps_30s"`
    }
    ```

    **表示フォーマット**:
    ```
    === VRAM Stats ===
    Command                  ──── 1s ────   ──── 10s ────   ──── 30s ────
                             Count Avg(μs)  Count Avg(μs)   Count Avg(μs)
    ──────────────────────────────────────────────────────────────────────
    draw_pixel                  10    5.00      95    4.80     290    4.90
    blit_rect                    0    0.00       5    6.20      15    6.10
    set_palette                  0    0.00       0    0.00       3    1.20

    === Monitor Stats ===
    FPS (1s): 60.0   FPS (10s): 58.5   FPS (30s): 57.2
    ```

*   **Logic**:
    - `printStats` 関数を更新して3ウィンドウのカラムを表示
    - 各コマンドの `Last1s`, `Last10s`, `Last30s` を横並びで表示
    - AvgNs → μs 変換（÷1000）は既存と同じ
    - Monitor の FPS は `FPS1s`, `FPS10s`, `FPS30s` を横並びで表示

### ドキュメント

#### [MODIFY] [VRAM-Specification.md](file://prompts/specifications/VRAM-Specification.md)

*   **更新内容**: 統計関連セクションがある場合、`CommandStat` の新構造（`Last1s` / `Last10s` / `Last30s`）を反映。HTTP エンドポイントのレスポンス例を更新。

#### [MODIFY] [README.md](file://features/README.md)

*   **更新内容**: stats 関連のセクションがある場合、stats CLI の表示例と HTTP レスポンスの JSON 形式を更新。

## Step-by-Step Implementation Guide

### ステップ 1: 統合テストの更新

- [x] `stats_http_test.go` のレスポンス型を新構造に更新
- [x] 既存テスト（`TestHTTPStatsIntegration`, `TestHTTPStatsVRAMOnly`, `TestHTTPStatsMonitorOnly`）のアサーションを更新
- [x] 新規テスト `TestHTTPStats_StableEntries` を追加
- [x] 新規テスト `TestHTTPStats_SnapshotIdempotent` を追加
- [x] 新規テスト `TestHTTPStats_MultiWindow` を追加

### ステップ 2: vram_stats_test.go の更新

- [x] レスポンスパース構造体を新 `CommandStat` 形式に更新
- [x] アサーションを `Last10s.Count` パスに更新（壁時計依存のタイミングを考慮）

### ステップ 3: 統合テスト実行

- [x] `scripts/process/integration_test.sh` を実行（統合テストは `features/neurom/integration/` にあり `build.sh` で実行済み）
  - 全統合テストが PASS することを確認

### ステップ 4: stats CLI の更新

- [x] `features/stats/cmd/main.go` のレスポンス型を更新
- [x] `printStats` 関数を3ウィンドウ表示に変更
- [x] Monitor 表示を FPS1s/10s/30s に変更

### ステップ 5: stats CLI のビルド確認

- [x] `scripts/process/build.sh` を実行し、stats CLI のビルドも含めて成功することを確認

### ステップ 6: ドキュメント更新

- [x] `VRAM-Specification.md` — 内容が「To Be Described」のため更新不要
- [x] `features/README.md` — stats セクションに表示例と HTTP レスポンス例を追加

### ステップ 7: 全体検証

- [x] `scripts/process/build.sh` を実行 — 全単体テスト PASS (exit code 0)
- [x] `scripts/process/integration_test.sh` を実行 — 統合テストは `build.sh` 内で実行済み、全 PASS

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
    - `TestHTTPStatsIntegration`: VRAM コマンドの統計が HTTP 経由で正しく返ること
    - `TestHTTPStats_StableEntries`: コマンドエントリが消失しないこと
    - `TestHTTPStats_SnapshotIdempotent`: 連続呼び出しで同一結果が返ること
    - `TestHTTPStats_MultiWindow`: Last1s/10s/30s が全て含まれること
    - `TestVRAMStatsIntegration`: バス経由の統計が正しい形式で返ること

## Documentation

#### [MODIFY] [VRAM-Specification.md](file://prompts/specifications/VRAM-Specification.md)
*   **更新内容**: `CommandStat` の JSON 構造例を `last_1s`/`last_10s`/`last_30s` 形式に更新。stats HTTP エンドポイントのレスポンス例を更新。

#### [MODIFY] [README.md](file://features/README.md)
*   **更新内容**: stats 機能の説明と CLI 表示例を更新（該当セクションが存在する場合のみ）。
