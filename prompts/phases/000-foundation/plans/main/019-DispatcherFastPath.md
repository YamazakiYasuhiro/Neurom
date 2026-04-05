# 019-DispatcherFastPath

> **Source Specification**: prompts/phases/000-foundation/ideas/main/019-DispatcherFastPath.md

## Goal Description

`parallelRows()` において、明らかに並列化不要な小データ操作に対するファストパスを導入し、`Decide()` / `Feedback()` / `time.Now()` のオーバーヘッドを排除する。加えて、single-worker 実行時の `Feedback()` をサンプリングベースに切り替え、ロック取得頻度を大幅に削減する。

## User Review Required

- `feedbackSampleRate = 64` の値。高すぎると EMA の追従が遅くなり、低すぎるとオーバーヘッド削減効果が薄い。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 要件1: ファストパスの導入 | Proposed Changes > vram.go — parallelRows() |
| 要件2: ファストパスの判定条件 (`dataSize < minThreshold`) | Proposed Changes > vram.go — parallelRows() |
| 要件3: Feedback サンプリング（single-worker） | Proposed Changes > dispatcher.go — Feedback(), cmdTracker |
| 要件4: 既存テストの互換性維持 | Verification Plan |
| 要件5 (任意): ロックフリー Decide | 将来検討。本計画では対象外 |

## Proposed Changes

### vram パッケージ (`features/neurom/internal/modules/vram/`)

#### [MODIFY] [dispatcher_test.go](file://features/neurom/internal/modules/vram/dispatcher_test.go)

*   **Description**: Feedback サンプリングの新規テストを追加。既存テストは変更なし。
*   **Test Cases (追加)**:

| テスト名 | 検証内容 |
| :--- | :--- |
| `TestFeedbackSampling_FirstCallProcessed` | `samples == 0` のとき、callCount に関わらず EMA が更新されること（ブートストラップ保証） |
| `TestFeedbackSampling_SkipsMostCalls` | `samples > 0` のとき、`feedbackSampleRate` 回に 1 回だけ EMA が更新されること |
| `TestFeedbackSampling_ParallelAlwaysProcessed` | `workers > 1` のとき、サンプリングに関係なく常に閾値調整が行われること |

*   **Logic**:

    **`TestFeedbackSampling_FirstCallProcessed`**:
    1. `newDynamicDispatcher(4)` で生成
    2. `tracker.samples` が 0 であることを確認
    3. `Feedback("blit_rect", 20000, 1, 200μs)` を 1 回呼ぶ
    4. `tracker.samples == 1` かつ `tracker.singleEMA > 0` を検証

    **`TestFeedbackSampling_SkipsMostCalls`**:
    1. `newDynamicDispatcher(4)` で生成
    2. `Feedback("blit_rect", 20000, 1, 200μs)` を 1 回呼んで EMA をブートストラップ
    3. `initialEMA := tracker.singleEMA` を記録
    4. `Feedback("blit_rect", 20000, 1, 異なるduration)` を `feedbackSampleRate - 1` 回呼ぶ（合計で callCount が feedbackSampleRate になる直前まで）
    5. `tracker.singleEMA == initialEMA` を検証（スキップされたため変化なし）
    6. もう 1 回 `Feedback()` を呼ぶ（callCount が feedbackSampleRate の倍数になる）
    7. `tracker.singleEMA != initialEMA` を検証（更新された）

    **`TestFeedbackSampling_ParallelAlwaysProcessed`**:
    1. `newDynamicDispatcher(4)` で生成
    2. `Feedback("clear_vram", 50000, 1, 500μs)` で EMA をブートストラップ
    3. `tracker.threshold` を記録
    4. `Feedback("clear_vram", 50000, 4, shortDuration)` を呼ぶ（ratio > speedupHigh になる値）
    5. `tracker.threshold` が減少したことを検証

#### [MODIFY] [dispatcher.go](file://features/neurom/internal/modules/vram/dispatcher.go)

*   **Description**: `cmdTracker` に `callCount` フィールドを追加。`Feedback()` に single-worker サンプリングロジックを追加。
*   **Technical Design**:

    **定数の追加**:
    ```go
    const (
        // ... (既存定数はそのまま)
        feedbackSampleRate = 64
    )
    ```

    **`cmdTracker` の変更**:
    ```go
    type cmdTracker struct {
        threshold int
        singleEMA float64
        samples   int
        callCount uint64
    }
    ```

*   **Logic — `Feedback()` の single-worker ブランチの変更**:

    現在のコード:
    ```go
    if workers == 1 {
        if tracker.samples == 0 {
            tracker.singleEMA = nsPerPixel
        } else {
            tracker.singleEMA = (1-feedbackAlpha)*tracker.singleEMA + feedbackAlpha*nsPerPixel
        }
        tracker.samples++
        return
    }
    ```

    変更後:
    ```go
    if workers == 1 {
        tracker.callCount++
        if tracker.samples > 0 && tracker.callCount%feedbackSampleRate != 0 {
            return
        }
        if tracker.samples == 0 {
            tracker.singleEMA = nsPerPixel
        } else {
            tracker.singleEMA = (1-feedbackAlpha)*tracker.singleEMA + feedbackAlpha*nsPerPixel
        }
        tracker.samples++
        return
    }
    ```

    ポイント:
    - `tracker.samples == 0` のときは常に処理する（EMA のブートストラップ保証）
    - `tracker.samples > 0` かつ `callCount % feedbackSampleRate != 0` のときはスキップ
    - parallel 実行（`workers > 1`）のブランチは変更なし

#### [MODIFY] [vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: `parallelRows()` にファストパスを追加。`dataSize < minThreshold` のとき、戦略に問い合わせずに直接実行する。
*   **Technical Design**:

    **`parallelRows()` の変更**:
    ```go
    func (v *VRAMModule) parallelRows(command string, totalRows int, width int, fn func(startRow, endRow int)) {
        if v.pool == nil || totalRows <= 1 {
            fn(0, totalRows)
            return
        }
        dataSize := totalRows * width
        if dataSize < minThreshold {
            fn(0, totalRows)
            return
        }
        workers := v.strategy.Decide(command, dataSize)
        start := time.Now()
        if workers == 1 {
            fn(0, totalRows)
        } else {
            chunks := splitRows(totalRows, workers)
            var wg sync.WaitGroup
            wg.Add(len(chunks))
            for _, c := range chunks {
                v.pool.tasks <- rowTask{fn: fn, startRow: c[0], endRow: c[1], wg: &wg}
            }
            wg.Wait()
        }
        v.strategy.Feedback(command, dataSize, workers, time.Since(start))
    }
    ```

    変更箇所: `dataSize := totalRows * width` の直後に `if dataSize < minThreshold` のガード節を追加。既存のロジックには一切手を入れない。

## Step-by-Step Implementation Guide

### ステップ 1: dispatcher_test.go — 新規テスト追加

- [x] `TestFeedbackSampling_FirstCallProcessed` を追加
- [x] `TestFeedbackSampling_SkipsMostCalls` を追加
- [x] `TestFeedbackSampling_ParallelAlwaysProcessed` を追加

### ステップ 2: dispatcher.go — サンプリング実装

- [x] `feedbackSampleRate = 64` 定数を追加
- [x] `cmdTracker` に `callCount uint64` フィールドを追加
- [x] `Feedback()` の single-worker ブランチにサンプリングガードを追加

### ステップ 3: vram.go — ファストパス追加

- [x] `parallelRows()` に `dataSize < minThreshold` のファストパスガード節を追加

### ステップ 4: ビルド確認

- [x] `scripts/process/build.sh` を実行
  - 新規テスト 3 件 PASS
  - 既存 dispatcher テスト 12 件 PASS
  - 既存 vram テスト全件 PASS
  - 統合テスト全件 PASS（ZMQ パニックは既知の既存問題）

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    - `dispatcher_test.go`: 既存 12 件 + 新規 3 件 = 15 件全 PASS
    - `vram_test.go`: 既存テスト全 PASS（変更なし）
    - `worker_test.go`: 既存テスト全 PASS（変更なし）
    - 統合テスト: 既存テスト全 PASS（API 互換性維持）

## Documentation

#### [MODIFY] [VRAM-Specification.md](file://prompts/specifications/VRAM-Specification.md)

*   **更新内容**: 内容が「To Be Described」のため、現時点では更新不要。
