# 019-DispatcherFastPath

## 背景 (Background)

018-VRAMDynamicWorkerStrategy で導入した `dynamicDispatcher` は、大データ操作（clear_vram, copy_rect）に対して効果的に動作している。しかし、小データ操作（blit_rect, blit_rect_transform）では、戦略判定自体のオーバーヘッドが処理本体を大きく上回っている。

### 計測データによる問題の裏付け

| コマンド | CPU=1 static 30s Avg(μs) | dynamic 30s Avg(μs) | オーバーヘッド |
|:---|---:|---:|:---|
| blit_rect | 1.91 | 10.92 | +9.01μs/回 |
| blit_rect_transform | 0.96 | 9.93 | +8.97μs/回 |
| clear_vram | 396.06 | 53.66 | なし（高速化） |
| copy_rect | 209.37 | 148.27 | なし（高速化） |

blit_rect は 30 秒間に約 49,000 回呼ばれる高頻度コマンドである。1 回あたり約 9μs のオーバーヘッドは、30 秒間で合計約 441ms のロスになる。

### オーバーヘッドの内訳

現在の `parallelRows()` は、`Decide()` が `workers=1` を返す場合でも以下を毎回実行している:

1. `Decide()`: ミューテックスロック取得 + 閾値比較 + ロック解放
2. `time.Now()`: システムコール
3. `fn(0, totalRows)`: 実際の処理（本体は 1〜2μs）
4. `time.Since(start)`: システムコール
5. `Feedback()`: ミューテックスロック取得 + EMA 計算 + ロック解放

CPU=1 に収まる処理（= 並列化しない処理）では、2〜5 のすべてが不要である。メインスレッドで直接 `fn()` を呼ぶだけで十分である。

## 要件 (Requirements)

### 必須要件

1. **ファストパスの導入**: `parallelRows()` において、明らかに並列化不要なデータサイズの操作は、`Decide()` / `Feedback()` / `time.Now()` を一切呼ばず、直接 `fn(0, totalRows)` を実行する

2. **ファストパスの判定条件**: `dataSize < minThreshold`（現在 64 ピクセル）の場合にファストパスを適用する
   - `minThreshold` は動的閾値の下限であり、どのコマンドの閾値もこれ以下にはならない
   - したがって、`dataSize < minThreshold` なら `Decide()` は常に `workers=1` を返す → 判定を省略しても結果は同じ

3. **Feedback スキップ（single-worker）**: `Decide()` が `workers=1` を返した場合、`Feedback()` の呼び出しを省略する
   - 根拠: `Feedback()` で single-worker の EMA を蓄積する意義は、将来の parallel 実行との比較にある。しかし閾値付近のデータサイズでは parallel 実行が発生するため、その時に single-worker EMA が必要になる
   - 解決策: **サンプリングベースの EMA 更新** — single-worker 実行時は N 回に 1 回だけ `Feedback()` を呼ぶ（例: 64 回に 1 回）。これにより EMA の精度を維持しつつオーバーヘッドを 1/64 に削減する
   - parallel 実行時（`workers > 1`）は常に `Feedback()` を呼ぶ（頻度が低いため）

4. **既存テストの互換性維持**: ファストパスの導入により既存の単体テスト・統合テストが壊れないこと

### 任意要件

5. **ロックフリー Decide**: `Decide()` 内で `sync/atomic` を使い、ロックを取らずに閾値を読み取る。閾値の更新は `Feedback()` のみで行われるため、ロックは `Feedback()` 側だけで十分な可能性がある（将来検討）

## 実現方針 (Implementation Approach)

### 変更箇所

#### 1. `parallelRows()` へのファストパス追加

```go
func (v *VRAMModule) parallelRows(command string, totalRows int, width int, fn func(startRow, endRow int)) {
    if v.pool == nil || totalRows <= 1 {
        fn(0, totalRows)
        return
    }
    dataSize := totalRows * width
    // Fast path: below absolute minimum threshold, no strategy overhead
    if dataSize < minThreshold {
        fn(0, totalRows)
        return
    }
    workers := v.strategy.Decide(command, dataSize)
    start := time.Now()
    if workers == 1 {
        fn(0, totalRows)
    } else {
        // ... existing pool dispatch logic
    }
    v.strategy.Feedback(command, dataSize, workers, time.Since(start))
}
```

#### 2. `Feedback()` のサンプリング

`dynamicDispatcher` に呼び出しカウンタを追加し、single-worker 実行時は N 回に 1 回だけ EMA を更新する:

```go
const feedbackSampleRate = 64 // single-worker 時: 64 回に 1 回だけ Feedback を処理

type cmdTracker struct {
    threshold   int
    singleEMA   float64
    samples     int
    callCount   uint64  // 追加: Feedback 呼び出しカウンタ
}
```

`Feedback()` の single-worker ブランチ:

```go
if workers == 1 {
    tracker.callCount++
    if tracker.callCount % feedbackSampleRate != 0 {
        return  // スキップ
    }
    // EMA 更新 (既存ロジック)
}
```

#### 3. `parallelRows()` での Feedback 呼び出し条件

ファストパスでない場合でも、`workers == 1` のときは Feedback をサンプリング判定に委ねる。ただし `Feedback()` 内部でサンプリングを行うため、`parallelRows()` 側の変更は不要（常に `Feedback()` を呼び、内部でスキップする）。

### 変更しない箇所

- `dispatchStrategy` インタフェースのシグネチャ
- `staticDispatcher` の実装
- `Decide()` のロジック（ファストパスは `parallelRows()` 側で行うため）
- テストの大半（ファストパスは `minThreshold` 未満の場合のみ作動し、既存テストのデータサイズは通常それ以上）

## 検証シナリオ (Verification Scenarios)

### シナリオ 1: ファストパスの動作確認

1. `-cpu 4 -vram-strategy dynamic` で neurom を起動する
2. デモプログラムをしばらく実行する（7 シーン循環を 1 周以上）
3. stats を取得する
4. blit_rect の 30s Avg(μs) が CPU=1 static の値（≈2μs）に近づいていることを確認する

### シナリオ 2: 大データ操作への影響なし

1. シナリオ 1 と同じ条件で stats を取得する
2. clear_vram, copy_rect の 30s Avg(μs) が前回の dynamic 結果（≈54μs, ≈148μs）と同等であることを確認する

### シナリオ 3: FPS の維持・改善

1. シナリオ 1 と同じ条件で stats を取得する
2. FPS(30s) が 58.0 以上であることを確認する

## テスト項目 (Testing for the Requirements)

### 単体テスト

| 要件 | テスト内容 | テストファイル |
|:---|:---|:---|
| 要件1 | `parallelRows()` に `dataSize < minThreshold` のデータを渡した場合、`Decide()` が呼ばれずに直接実行されること | `dispatcher_test.go` または `vram_test.go` |
| 要件2 | `minThreshold` 未満のデータサイズで `fn()` が正しく `(0, totalRows)` で呼ばれること | `vram_test.go` |
| 要件3 | `Feedback()` の single-worker サンプリングが正しく動作すること（N 回に 1 回だけ EMA 更新） | `dispatcher_test.go` |
| 要件4 | 既存の dispatcher テスト 12 件が全 PASS すること | `dispatcher_test.go` |
| 要件4 | 既存の vram テスト全件が全 PASS すること | `vram_test.go` |

### 検証コマンド

```bash
# 全体ビルド + 単体テスト
./scripts/process/build.sh

# 統合テスト
./scripts/process/integration_test.sh
```
