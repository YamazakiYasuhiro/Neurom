# 014-VRAMMultiCore

> **Source Specification**: `prompts/phases/000-foundation/ideas/main/014-VRAMMultiCore.md`

## Goal Description

VRAMモジュールの描画処理（`clear_vram`, `blit_rect`, `blit_rect_transform`, `copy_rect`）を行分割によるマルチコア並列実行に対応させる。ワーカーgoroutineプールを導入し、起動オプション `--cpu N` で並列度を指定可能にする。`N=1`（デフォルト）では逐次実行を維持し、既存動作との完全互換を保証する。

**前提**: 016-VRAMFeatureReintegration Part 1 + Part 2 の完了後に実施する。全描画コマンドがページ対応済みであること。

## User Review Required

- `vram.New()` のシグネチャを `vram.New(cfgs ...Config)` に変更する（可変長引数で後方互換維持）。既存の `New()` 呼び出し箇所は変更不要。
- `--cpu` フラグは Part 3（`cmd/main.go` 修正）で追加する。本計画ではVRAMモジュール内部の並列化のみを対象とする。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
|:---|:---|
| 1: 起動オプション `--cpu N` | Part 3 (016-Part3) > cmd/main.go へ委譲。本計画では Config.Workers で受け取り |
| 2: goroutine ワーカープールの実装 | Proposed Changes > worker.go — workerPool |
| 3: VRAMループ処理の行分割による並列化 | Proposed Changes > vram.go — parallelRows |
| 4: 並列化対象の操作 (clear/blit/transform/copy) | Proposed Changes > vram.go — 各ハンドラ修正 |
| 4: TransformBlit の並列化 | Proposed Changes > transform.go — TransformBlitParallel |
| 5: 既存動作との互換性 | Verification Plan — workers=1 テスト |

## Proposed Changes

### vram モジュール

#### [NEW] [features/neurom/internal/modules/vram/worker_test.go](file://features/neurom/internal/modules/vram/worker_test.go)

* **Description**: ワーカープールと行分割ユーティリティの単体テスト（TDD）
* **Technical Design**:

  ```go
  func TestSplitRows(t *testing.T)
  // テーブル駆動テスト:
  // | totalRows | numWorkers | expected |
  // | 212       | 4          | [[0,53],[53,106],[106,159],[159,212]] |
  // | 212       | 1          | [[0,212]] |
  // | 3         | 8          | [[0,1],[1,2],[2,3]] (行数 < ワーカー数) |
  // | 1         | 4          | [[0,1]] |
  // | 0         | 4          | [] (空) |
  // - 全チャンクが隙間なく連続すること（startRow[i+1] == endRow[i]）
  // - 最後のチャンクの endRow == totalRows であること

  func TestWorkerPoolStartStop(t *testing.T)
  // - newWorkerPool(4) でプール起動
  // - 簡単なタスクを投入して完了を確認
  // - pool.stop() で正常終了
  // - goroutine リークがないこと（runtime.NumGoroutine で前後比較）

  func TestWorkerPoolExecution(t *testing.T)
  // - プール(4)に行範囲タスクを投入
  // - 全行がちょうど1回ずつ処理されること（atomic カウンタで検証）
  // - 結果が逐次実行と一致すること
  ```

#### [NEW] [features/neurom/internal/modules/vram/worker.go](file://features/neurom/internal/modules/vram/worker.go)

* **Description**: ワーカープール実装（goroutine プール + 行分割ユーティリティ）
* **Technical Design**:

  ```go
  type rowTask struct {
      fn       func(startRow, endRow int)
      startRow int
      endRow   int
      wg       *sync.WaitGroup
  }

  type workerPool struct {
      tasks chan rowTask
      quit  chan struct{}
      size  int
      wg    sync.WaitGroup
  }

  func newWorkerPool(size int) *workerPool
  // - size 個の goroutine を起動
  // - 各 goroutine は tasks チャネルから rowTask を受信して fn(startRow, endRow) を実行
  // - 実行後に task.wg.Done() を呼ぶ
  // - quit チャネルが閉じられたら goroutine 終了

  func (p *workerPool) stop()
  // - close(p.quit) で全 goroutine に終了を通知
  // - p.wg.Wait() で全 goroutine の完了を待機
  ```

  ```go
  // splitRows divides totalRows into numWorkers contiguous chunks.
  // Each chunk is [startRow, endRow). If totalRows < numWorkers,
  // only totalRows chunks are returned (one row each).
  func splitRows(totalRows, numWorkers int) [][2]int
  ```

  **splitRows のロジック:**
  - `numWorkers > totalRows` の場合、`numWorkers = totalRows` に制限
  - `base = totalRows / numWorkers`、`remainder = totalRows % numWorkers`
  - 先頭 `remainder` チャンクは `base + 1` 行、残りは `base` 行
  - 返り値: `[][2]int` — 各要素は `[startRow, endRow)`

* **Logic**:
  - ワーカー goroutine のメインループ:
    ```go
    func (p *workerPool) worker() {
        defer p.wg.Done()
        for {
            select {
            case <-p.quit:
                return
            case task := <-p.tasks:
                task.fn(task.startRow, task.endRow)
                task.wg.Done()
            }
        }
    }
    ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

* **Description**: マルチコア時の描画結果が逐次実行と一致することを検証するテストを追加
* **Technical Design**:

  ```go
  func TestClearVRAMMultiCore(t *testing.T)
  // - workers=1 と workers=4 で同一の clear_vram を実行
  // - indexBuffer, colorBuffer が完全一致すること

  func TestBlitRectMultiCore(t *testing.T)
  // - workers=1 と workers=4 で同一の blit_rect (Replace, Alpha) を実行
  // - indexBuffer, colorBuffer が完全一致すること

  func TestBlitRectTransformMultiCore(t *testing.T)
  // - workers=1 と workers=4 で同一の blit_rect_transform を実行
  // - indexBuffer, colorBuffer が完全一致すること

  func TestCopyRectMultiCore(t *testing.T)
  // - workers=1 と workers=4 で同一の copy_rect を実行
  // - indexBuffer, colorBuffer が完全一致すること

  func TestCPUConfigValidation(t *testing.T)
  // - Config{Workers: 0} → workers=1 にフォールバック
  // - Config{Workers: 257} → workers=1 にフォールバック
  // - Config{Workers: 256} → 正常に 256 が設定されること
  ```

  **テストヘルパー:**
  ```go
  func newTestVRAMWithWorkers(workers int) *VRAMModule
  // Config{Workers: workers} で VRAMModule を生成
  ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

* **Description**: Config 構造体追加、ワーカープール統合、各描画操作に `parallelRows` ディスパッチを適用
* **Technical Design**:

  **Config 構造体:**
  ```go
  type Config struct {
      Workers int // Number of worker goroutines (default: 1, range: 1-256)
  }
  ```

  **VRAMModule の変更:**
  ```go
  type VRAMModule struct {
      // ... 既存フィールド (Part 2 完了後の状態) ...
      pool *workerPool // nil when Workers=1
  }

  func New(cfgs ...Config) *VRAMModule {
      cfg := Config{Workers: 1}
      if len(cfgs) > 0 {
          cfg = cfgs[0]
      }
      if cfg.Workers < 1 || cfg.Workers > 256 {
          cfg.Workers = 1
      }

      v := &VRAMModule{
          pages: []pageBuffer{newPageBuffer(DefaultPageWidth, DefaultPageHeight)},
          stats: stats.NewCollector(),
      }
      if cfg.Workers > 1 {
          v.pool = newWorkerPool(cfg.Workers)
      }
      return v
  }
  ```

  **Stop() の変更:**
  ```go
  func (v *VRAMModule) Stop() error {
      log.Println("[VRAM] Stop: waiting for run goroutine...")
      v.wg.Wait()
      if v.pool != nil {
          v.pool.stop()
      }
      log.Println("[VRAM] Stop: run goroutine finished.")
      return nil
  }
  ```

  **parallelRows ディスパッチ関数:**
  ```go
  // parallelRows splits totalRows across workers and executes fn for each chunk.
  // If pool is nil (single-core mode), fn is called directly with (0, totalRows).
  func (v *VRAMModule) parallelRows(totalRows int, fn func(startRow, endRow int)) {
      if v.pool == nil || totalRows <= 1 {
          fn(0, totalRows)
          return
      }
      chunks := splitRows(totalRows, v.pool.size)
      var wg sync.WaitGroup
      wg.Add(len(chunks))
      for _, c := range chunks {
          v.pool.tasks <- rowTask{fn: fn, startRow: c[0], endRow: c[1], wg: &wg}
      }
      wg.Wait()
  }
  ```

* **Logic — 各描画操作への適用:**

  **`handleClearVRAM`**: 行分割で index/color バッファを並列塗りつぶし
  ```go
  page := &v.pages[pageIdx]
  v.parallelRows(page.height, func(startRow, endRow int) {
      for y := startRow; y < endRow; y++ {
          for x := 0; x < page.width; x++ {
              idx := y*page.width + x
              page.index[idx] = paletteIdx
              page.color[idx*4]   = pal[0]
              page.color[idx*4+1] = pal[1]
              page.color[idx*4+2] = pal[2]
              page.color[idx*4+3] = pal[3]
          }
      }
  })
  ```

  **`handleBlitRect`**: クリップ後の描画行を分割
  ```go
  v.parallelRows(ch, func(startRow, endRow int) {
      for row := startRow; row < endRow; row++ {
          srcY := srcOffY + row
          dstY := cy + row
          for col := 0; col < cw; col++ {
              srcX := srcOffX + col
              srcIdx := pixelData[srcY*w + srcX]
              dstIdx := dstY*page.width + (cx + col)
              // Replace or Blend (same logic as Part 1, but per-row)
          }
      }
  })
  ```

  **`handleBlitRectTransform`**: TransformBlit の結果バッファへの書き込みを行分割
  ```go
  // TransformBlit で変換後バッファを取得（これ自体も並列化）
  // 変換結果をVRAMに書き込む部分を parallelRows で分割
  v.parallelRows(dstH, func(startRow, endRow int) {
      for outY := startRow; outY < endRow; outY++ {
          for outX := 0; outX < dstW; outX++ {
              // pixel write logic (same as Part 1, but per-row)
          }
      }
  })
  ```

  **`handleCopyRect`**: 一時バッファへの読み出しと宛先への書き込みを各行分割
  ```go
  // Phase 1: Read into temp buffer (parallel)
  v.parallelRows(ch, func(startRow, endRow int) {
      for row := startRow; row < endRow; row++ {
          // copy from srcPage to temp
      }
  })
  // Phase 2: Write from temp to destination (parallel)
  v.parallelRows(ch, func(startRow, endRow int) {
      for row := startRow; row < endRow; row++ {
          // copy from temp to dstPage
      }
  })
  ```

  **注意**: `mu.Lock()` は `handleMessage` レベルで保持済みなので、ワーカー内でのロックは不要。パレットは読み取り専用参照。行範囲は重複しないため書き込み競合なし。

#### [MODIFY] [features/neurom/internal/modules/vram/transform.go](file://features/neurom/internal/modules/vram/transform.go)

* **Description**: `TransformBlit` に並列化対応を追加
* **Technical Design**:

  ```go
  // TransformBlitParallel is the parallelized version of TransformBlit.
  // parallelFn dispatches row ranges to a worker pool.
  // If parallelFn is nil, the function falls back to sequential execution.
  func TransformBlitParallel(
      src []uint8, srcW, srcH int,
      pivotX, pivotY int,
      rotation uint8,
      scaleX, scaleY uint16,
      parallelFn func(totalRows int, fn func(startRow, endRow int)),
  ) (dst []uint8, dstW, dstH, offsetX, offsetY int)
  ```

* **Logic**:
  - バウンディングボックスの計算は既存の TransformBlit と同一（逐次実行）
  - 出力バッファの割り当ても逐次実行
  - 逆変換ループ（行方向）を `parallelFn` で分割:
    ```go
    fill := func(startRow, endRow int) {
        for outY := startRow; outY < endRow; outY++ {
            for outX := range dstW {
                // existing inverse transform logic
            }
        }
    }
    if parallelFn != nil {
        parallelFn(dstH, fill)
    } else {
        fill(0, dstH)
    }
    ```
  - 既存の `TransformBlit` 関数は `TransformBlitParallel(..., nil)` を呼ぶラッパーとして維持（後方互換）

### 統合テスト

#### [NEW] [features/neurom/integration/vram_multicore_test.go](file://features/neurom/integration/vram_multicore_test.go)

* **Description**: Bus経由でのマルチコア描画パイプラインの統合テスト
* **Technical Design**:
  ```go
  func TestMultiCoreClearAndBlit(t *testing.T)
  // - Config{Workers: 4} で VRAM を起動
  // - clear_vram → blit_rect → read_rect の結果を検証
  // - Workers=1 の結果と比較して完全一致

  func TestMultiCoreBlendModes(t *testing.T)
  // - Config{Workers: 4} で VRAM を起動
  // - Alpha/Additive/Multiply/Screen の各モードで blit_rect 実行
  // - Workers=1 の結果と比較して完全一致

  func TestMultiCoreShutdown(t *testing.T)
  // - Config{Workers: 4} で起動後、即座にシャットダウン
  // - goroutine リークやハングが発生しないこと
  ```

## Step-by-Step Implementation Guide

- [x] 1. **worker_test.go 作成**: `TestSplitRows`, `TestWorkerPoolStartStop`, `TestWorkerPoolExecution` を追加（TDD）
- [x] 2. **worker.go 作成**: `rowTask`, `workerPool`, `splitRows` を実装 → テストパス確認
- [x] 3. **ビルド確認**: `./scripts/process/build.sh`
- [x] 4. **vram_test.go にマルチコアテスト追加**: `TestCPUConfigValidation`, `TestClearVRAMMultiCore`
- [x] 5. **vram.go に Config 構造体追加**: `New(cfgs ...Config)` シグネチャ変更、`pool` フィールド追加、`Stop()` にプール停止追加
- [x] 6. **parallelRows 関数を実装**
- [x] 7. **handleClearVRAM を parallelRows で並列化** → `TestClearVRAMMultiCore` パス確認
- [x] 8. **vram_test.go に `TestBlitRectMultiCore` を追加**
- [x] 9. **handleBlitRect を parallelRows で並列化** → テストパス確認
- [x] 10. **transform.go に `TransformBlitParallel` を追加**: 既存 `TransformBlit` をラッパー化
- [x] 11. **vram_test.go に `TestBlitRectTransformMultiCore` を追加**
- [x] 12. **handleBlitRectTransform を並列化** → テストパス確認
- [x] 13. **vram_test.go に `TestCopyRectMultiCore` を追加**
- [x] 14. **handleCopyRect を並列化**（2段階: 読み出し→書き込み）→ テストパス確認
- [x] 15. **ビルド確認**: `./scripts/process/build.sh` — 全単体テスト（既存 + マルチコア）がパスすること
- [x] 16. **統合テスト追加**: `vram_multicore_test.go`
- [x] 17. **統合テスト確認**: マルチコア統合テスト全パス
- [x] 18. **全テスト確認**: `./scripts/process/build.sh` 全パス確認済

## Verification Plan

### Automated Verification

1. **Build & Unit Tests**:
   ```bash
   ./scripts/process/build.sh
   ```
   - vram パッケージの全テスト（Part 1 + Part 2 + マルチコア）がパスすること
   - transform_test.go の既存テストがパスすること（TransformBlit ラッパー互換）
   - Workers=1 で全既存テストが変更なしで通ること

2. **Integration Tests**:
   ```bash
   ./scripts/process/integration_test.sh --specify TestMultiCore
   ./scripts/process/integration_test.sh
   ```
   - マルチコア（Workers=4）の描画結果が逐次実行と完全一致すること
   - 既存テスト（`TestDemoProgram`, `TestBlitAndReadRect` 等）がリグレッションなく通ること

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/plans/main/014-VRAMMultiCore.md](file://prompts/phases/000-foundation/plans/main/014-VRAMMultiCore.md)
* **更新内容**: 各ステップの `[ ]` → `[x]` で進捗管理
