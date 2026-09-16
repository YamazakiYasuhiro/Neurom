# 018-VRAMDynamicWorkerStrategy

> **Source Specification**: prompts/phases/000-foundation/ideas/main/018-VRAMDynamicWorkerStrategy.md

## Goal Description

VRAM の並列化戦略を `static`（従来の固定割り当て）と `dynamic`（データサイズ＋パフォーマンスフィードバックによる動的割り当て）の2つから CLI フラグで選択可能にする。`parallelRows()` を拡張し、`dispatchStrategy` インタフェースを通じてコマンド別の閾値判定とフィードバックループを実現する。

## User Review Required

- `parallelRows()` のシグネチャ変更（`command string` 引数の追加）により、内部の全呼び出し箇所を修正する必要がある。外部公開 API には影響なし。
- `TransformBlitParallel` の `parallelFn` 引数型は変更しない（内部で command 名をキャプチャする）。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| 要件1: 戦略の切り替え CLI フラグ | Proposed Changes > cmd/main.go, vram/vram.go Config |
| 要件2: 動的ワーカー割り当て | Proposed Changes > vram/dispatcher.go — dynamicDispatcher |
| 要件3: 閾値ベースの判定 | Proposed Changes > vram/dispatcher.go — Decide() |
| 要件4: パフォーマンスフィードバック | Proposed Changes > vram/dispatcher.go — Feedback(), cmdTracker |
| 要件5: static 戦略の保持 | Proposed Changes > vram/dispatcher.go — staticDispatcher |
| 要件6: 段階的 goroutine 数（任意） | 将来拡張。Decide() の戻り値が int のため、構造として対応可能 |

## Proposed Changes

### vram パッケージ (`features/neurom/internal/modules/vram/`)

#### [NEW] [dispatcher_test.go](file://features/neurom/internal/modules/vram/dispatcher_test.go)

*   **Description**: dispatchStrategy の各実装に対する単体テスト。TDD のため実装に先立って作成する。
*   **Test Cases**:

| テスト名 | 検証内容 |
| :--- | :--- |
| `TestStaticDispatcher_AlwaysReturnsMax` | staticDispatcher.Decide() がコマンドやデータサイズに関係なく常に maxWorkers を返すこと |
| `TestStaticDispatcher_FeedbackNoop` | staticDispatcher.Feedback() を呼んでも Decide() の結果が変わらないこと |
| `TestDynamicDispatcher_BelowThreshold` | データサイズが閾値未満のとき workers=1 を返すこと |
| `TestDynamicDispatcher_AboveThreshold` | データサイズが閾値以上のとき workers=maxWorkers を返すこと |
| `TestDynamicDispatcher_UnknownCommand` | 未登録コマンドに対してデフォルト閾値を適用すること |
| `TestDynamicDispatcher_FeedbackIncreasesThreshold` | 並列実行でスピードアップが小さい場合（ratio < 1.5）、閾値が上昇すること |
| `TestDynamicDispatcher_FeedbackDecreasesThreshold` | 並列実行でスピードアップが大きい場合（ratio > 2.0）、閾値が下降すること |
| `TestDynamicDispatcher_ThresholdBounds` | 閾値が minThreshold 未満に下がらず、maxThreshold を超えないこと |
| `TestDynamicDispatcher_SingleWorkerNoFeedback` | workers=1 のとき Feedback() が閾値を変更しないこと（シングル実行時は閾値調整対象外） |
| `TestNewDispatchStrategy_Static` | `newDispatchStrategy("static", 4)` が staticDispatcher を返すこと |
| `TestNewDispatchStrategy_Dynamic` | `newDispatchStrategy("dynamic", 4)` が dynamicDispatcher を返すこと |
| `TestNewDispatchStrategy_Default` | `newDispatchStrategy("", 4)` が dynamicDispatcher を返すこと（デフォルト） |

#### [NEW] [dispatcher.go](file://features/neurom/internal/modules/vram/dispatcher.go)

*   **Description**: 並列化戦略のインタフェースと2つの実装を提供する。
*   **Technical Design**:

    **インタフェース**:
    ```go
    type dispatchStrategy interface {
        Decide(command string, dataSize int) int
        Feedback(command string, dataSize int, workers int, elapsed time.Duration)
    }
    ```

    **ファクトリ関数**:
    ```go
    func newDispatchStrategy(strategy string, maxWorkers int) dispatchStrategy
    // strategy == "static" → &staticDispatcher{maxWorkers}
    // それ以外（"dynamic", ""） → newDynamicDispatcher(maxWorkers)
    ```

    **staticDispatcher**:
    ```go
    type staticDispatcher struct {
        maxWorkers int
    }
    // Decide: 常に maxWorkers を返す
    // Feedback: no-op
    ```

    **dynamicDispatcher**:
    ```go
    const (
        minThreshold     = 64      // 8x8 ピクセル
        maxThreshold     = 262144  // 512x512 ピクセル
        defaultThreshold = 16384   // 128x128 ピクセル
        feedbackAlpha    = 0.1     // EMA smoothing factor
        speedupLow       = 1.5    // この比率未満 → 閾値引き上げ
        speedupHigh      = 2.0    // この比率超 → 閾値引き下げ
        thresholdStep    = 0.2    // 閾値変更時の倍率 (±20%)
    )

    type cmdTracker struct {
        threshold   int
        singleEMA   float64  // シングル実行時の ns/pixel の指数移動平均
        samples     int      // singleEMA の有効サンプル数
    }

    type dynamicDispatcher struct {
        maxWorkers int
        trackers   map[string]*cmdTracker
        mu         sync.Mutex
    }
    ```

*   **Logic**:

    **`newDynamicDispatcher(maxWorkers)`**:
    - trackers を初期化。既知コマンドの初期閾値:
      - `"clear_vram"`: 4096
      - `"blit_rect"`: 16384
      - `"blit_rect_transform"`: 32768
      - `"copy_rect"`: 8192

    **`Decide(command, dataSize)`**:
    1. ロック取得
    2. `trackers[command]` を取得。存在しなければ `&cmdTracker{threshold: defaultThreshold}` を作成して登録
    3. `dataSize < tracker.threshold` → return 1
    4. otherwise → return maxWorkers

    **`Feedback(command, dataSize, workers, elapsed)`**:
    1. `workers == 1` のとき:
       - `nsPerPixel = elapsed.Nanoseconds() / int64(dataSize)` （dataSize > 0 の場合のみ）
       - `tracker.singleEMA` を更新: `singleEMA = (1-alpha)*singleEMA + alpha*nsPerPixel`（samples == 0 なら直接代入）
       - `tracker.samples++`
       - return（閾値調整は行わない）
    2. `workers > 1` かつ `tracker.samples > 0` かつ `dataSize > 0` のとき:
       - `parallelNsPerPixel = elapsed.Nanoseconds() / int64(dataSize)`
       - `ratio = tracker.singleEMA / parallelNsPerPixel`
       - `ratio < speedupLow` → `tracker.threshold = min(int(float64(tracker.threshold) * (1+thresholdStep)), maxThreshold)`
       - `ratio > speedupHigh` → `tracker.threshold = max(int(float64(tracker.threshold) * (1-thresholdStep)), minThreshold)`
       - （speedupLow <= ratio <= speedupHigh の場合は閾値変更なし）

#### [MODIFY] [vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: Config に Strategy フィールドを追加。VRAMModule に strategy フィールドを追加。parallelRows のシグネチャを拡張。
*   **Technical Design**:

    **Config の変更**:
    ```go
    type Config struct {
        Workers  int    // 1-256, default 1
        Strategy string // "static" or "dynamic", default "dynamic"
    }
    ```

    **VRAMModule 構造体の変更**:
    ```go
    type VRAMModule struct {
        // ... (既存フィールド)
        pool     *workerPool
        strategy dispatchStrategy  // 追加
    }
    ```

    **New() の変更**:
    ```go
    func New(cfgs ...Config) *VRAMModule {
        // ... Workers バリデーション (既存)
        v := &VRAMModule{...}
        if cfg.Workers > 1 {
            v.pool = newWorkerPool(cfg.Workers)
            v.strategy = newDispatchStrategy(cfg.Strategy, cfg.Workers)
        }
        return v
    }
    ```

    **parallelRows の変更**:
    ```go
    func (v *VRAMModule) parallelRows(command string, totalRows int, width int, fn func(startRow, endRow int))
    ```
    - 第1引数に `command string`、第3引数に `width int` を追加
    - `dataSize = totalRows * width`
    - `v.pool == nil || totalRows <= 1` → `fn(0, totalRows); return`
    - `workers := v.strategy.Decide(command, dataSize)`
    - `start := time.Now()`
    - `workers == 1` → `fn(0, totalRows)`
    - `workers > 1` → 既存のプール分割ロジック
    - `v.strategy.Feedback(command, dataSize, workers, time.Since(start))`

    **parallelRowsFn の変更**:
    ```go
    func (v *VRAMModule) parallelRowsFn(command string) func(int, func(int, int))
    ```
    - command をキャプチャして返すクロージャ内で `v.parallelRows(command, totalRows, ?, fn)` を呼ぶ
    - ただし TransformBlitParallel は srcW を width として渡すべきだが、parallelRowsFn のシグネチャは `func(int, func(int, int))` のまま維持する必要がある
    - 解決策: width を引数に追加 → `parallelRowsFn(command string, width int)` として、クロージャ内で width を固定

*   **Logic — 呼び出し箇所の修正**:

    | メソッド | 変更前 | 変更後 |
    | :--- | :--- | :--- |
    | handleClearVRAM | `v.parallelRows(pg.height, fn)` | `v.parallelRows("clear_vram", pg.height, pg.width, fn)` |
    | handleBlitRect | `v.parallelRows(ch, fn)` | `v.parallelRows("blit_rect", ch, cw, fn)` |
    | handleBlitRectTransform (outH) | `v.parallelRows(outH, fn)` | `v.parallelRows("blit_rect_transform", outH, outW, fn)` |
    | handleBlitRectTransform (transform call) | `v.parallelRowsFn()` | `v.parallelRowsFn("blit_rect_transform", srcW)` |
    | handleCopyRect (read) | `v.parallelRows(csh, fn)` | `v.parallelRows("copy_rect", csh, csw, fn)` |
    | handleCopyRect (write) | `v.parallelRows(cdh, fn)` | `v.parallelRows("copy_rect", cdh, cdw, fn)` |

#### [MODIFY] [vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: Config の Strategy フィールドのテスト追加、既存テストでの互換性確認。
*   **Changes**:
    - `TestCPUConfigValidation`: Strategy フィールドのテストケース追加
      - `Strategy: ""` → dynamicDispatcher が生成されること
      - `Strategy: "static"` → staticDispatcher が生成されること
      - `Strategy: "dynamic"` → dynamicDispatcher が生成されること
    - 既存の `newTestVRAM()` はデフォルト Config（Workers=1）で strategy が nil のまま → 影響なし

### CLI (`features/neurom/cmd/`)

#### [MODIFY] [main.go](file://features/neurom/cmd/main.go)

*   **Description**: `-vram-strategy` フラグの追加。
*   **Logic**:
    - `flag.String("vram-strategy", "dynamic", "VRAM parallelization strategy: static or dynamic")` を追加
    - `vram.New(vram.Config{Workers: *cpuWorkers, Strategy: *vramStrategy})` に渡す

## Step-by-Step Implementation Guide

### ステップ 1: dispatcher テスト作成

- [x] `dispatcher_test.go` を新規作成
  - 12 テストケースを記述
  - `dispatchStrategy` インタフェースと `newDispatchStrategy` のテスト
  - staticDispatcher のテスト
  - dynamicDispatcher の閾値判定テスト
  - フィードバックによる閾値増減テスト
  - 閾値の上下限バウンダリテスト

### ステップ 2: dispatcher 実装

- [x] `dispatcher.go` を新規作成
  - `dispatchStrategy` インタフェース定義
  - `staticDispatcher` 実装
  - `dynamicDispatcher` 実装（cmdTracker, Decide, Feedback）
  - `newDispatchStrategy` ファクトリ関数

### ステップ 3: vram.go の修正

- [x] `Config` に `Strategy string` フィールドを追加
- [x] `VRAMModule` に `strategy dispatchStrategy` フィールドを追加
- [x] `New()` で `newDispatchStrategy()` を呼び出す
- [x] `parallelRows()` のシグネチャを変更（command, width 追加）
- [x] `parallelRowsFn()` のシグネチャを変更（command, width 追加）
- [x] 全 `parallelRows` / `parallelRowsFn` 呼び出し箇所を更新（6箇所）

### ステップ 4: vram_test.go の修正

- [x] `TestCPUConfigValidation` に Strategy テストケースを追加

### ステップ 5: main.go の修正

- [x] `-vram-strategy` フラグを追加
- [x] `vram.New()` への Config 渡しを更新

### ステップ 6: ビルド確認

- [x] `scripts/process/build.sh` を実行
  - 全単体テスト PASS 確認済み
  - 統合テスト PASS 確認済み（ZMQ パニックは既知の既存問題、今回の変更とは無関係）

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```
    - `dispatcher_test.go`: 12 テストケース全 PASS
    - `vram_test.go`: 既存テスト + Strategy テストケース全 PASS
    - `worker_test.go`: 既存テスト全 PASS（変更なし）
    - 統合テスト: 既存テスト全 PASS（API 互換性維持）

## Documentation

#### [MODIFY] [VRAM-Specification.md](file://prompts/specifications/VRAM-Specification.md)

*   **更新内容**: 内容が「To Be Described」のため、現時点では更新不要。

#### [MODIFY] [README.md](file://features/README.md)

*   **更新内容**: neurom セクションの実行例に `-vram-strategy` オプションの説明を追加。
