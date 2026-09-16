# 018-VRAMDynamicWorkerStrategy

## 背景 (Background)

現在の VRAM モジュールは `-cpu N` オプションで指定した固定数の goroutine をワーカープールとして確保し、`parallelRows()` を通じてピクセル操作を並列化している。しかし、実際の計測データから以下の課題が明らかになった。

### 参考計測データ: CPU=1 (static) vs CPU=4 (static)

以下は、デモプログラム（7シーン循環）の実行中に `-vram-strategy static` 相当の固定割り当てで計測した実データである。dynamic 戦略のパフォーマンス評価時のベースラインとして使用する。

#### CPU=1 の全コマンド統計

```
Command                  ──── 1s ────   ──── 10s ────   ──── 30s ────
                         Count Avg(μs)  Count Avg(μs)   Count Avg(μs)
──────────────────────────────────────────────────────────────────────
blit_rect                  178   10.99  69381    1.95   140174    1.91
blit_rect_transform        270    2.80   1128    0.67    2256    0.96
clear_vram                  92  464.93    756  471.74    1727  396.06
copy_rect                    0    0.00      0    0.00    1128  209.37
display_page_changed         1    0.00      3    0.00     385    0.00
mode                         0    0.00      0    0.00       0    0.00
mode_changed                 0    0.00      0    0.00       0    0.00
page_count_changed           1    0.00      3    0.00      10    0.00
page_size_changed            0    0.00      0    0.00       1    0.00
palette_block_updated        1    0.00      5    0.00      11    0.00
rect_copied                  0    0.00      0    0.00    1128    0.00
rect_updated               448    0.00  50901    0.07   103230    0.05
set_display_page             1    0.00      3    0.00     385    0.00
set_page_count               1    0.00      3    0.00      10    0.00
set_page_size                0    0.00      0    0.00       1    0.00
set_palette_block            1    0.00      5    0.00      11    0.00
set_viewport                 1    0.00     92    0.00     384    0.00
viewport_changed             1    0.00     92    0.00     384    0.00
vram_cleared                92    0.00    756    0.00    1727    0.00
FPS (1s): 60.0   FPS (10s): 60.7   FPS (30s): 58.2
```

#### CPU=4 の全コマンド統計

```
Command                  ──── 1s ────   ──── 10s ────   ──── 30s ────
                         Count Avg(μs)  Count Avg(μs)   Count Avg(μs)
──────────────────────────────────────────────────────────────────────
blit_rect                11305    5.57  47154    7.39   97543    7.11
blit_rect_transform        114    9.55    114    9.55    1239    5.17
clear_vram                  38   68.25    503   90.09    1551   53.27
copy_rect                    0    0.00    170  278.86    1127  251.00
display_page_changed         1    0.00     90    0.00     385    0.00
mode                         0    0.00      0    0.00       1    0.00
mode_changed                 0    0.00      0    0.00       1    0.00
page_count_changed           0    0.00      4    0.00       9    0.00
page_size_changed            0    0.00      1    0.00       1    0.00
palette_block_updated        1    0.00      5    0.00      12    0.00
rect_copied                  0    0.00    170    0.00    1127    0.00
rect_updated              8829    0.09  37929    0.08   79448    0.07
set_display_page             1    0.00     90    0.00     385    0.00
set_page_count               0    0.00      4    0.00       9    0.00
set_page_size                0    0.00      1  522.60       1  522.60
set_palette_block            1    0.00      5    0.00      12    0.00
set_viewport                 0    0.00    378    0.00     382    0.00
viewport_changed             0    0.00    378    0.00     382    0.00
vram_cleared                38    0.00    503    0.00    1551    0.00
FPS (1s): 60.0   FPS (10s): 60.0   FPS (30s): 54.8
```

#### 主要コマンドの比較サマリ

| コマンド | CPU=1 30s Avg(μs) | CPU=4 30s Avg(μs) | 高速化率 | 備考 |
|:---|---:|---:|---:|:---|
| clear_vram | 396.06 | 53.27 | 7.4x | 全ページクリア。並列化効果大 |
| blit_rect | 1.91 | 7.11 | 0.3x (悪化) | 小さい blit が多数。並列化オーバーヘッドが支配的 |
| blit_rect_transform | 0.96 | 5.17 | 0.2x (悪化) | 同上。変換付きの小 blit |
| copy_rect | 209.37 | 251.00 | 0.8x (微悪化) | 大きいが CPU=4 でやや悪化 |

注目すべき点:
- **clear_vram**: 大きなデータ量（256×212 = 54,272 ピクセル全クリア）で 7.4x の顕著な高速化
- **blit_rect / blit_rect_transform**: 30s 平均で見ると CPU=4 で悪化。小さい blit（数ピクセル〜数十行）が大半を占め、goroutine 起動・同期コストが処理コストを上回る
- **copy_rect**: 大きなデータだが CPU=4 で微悪化。ロック競合やメモリ帯域の影響の可能性
- **FPS**: CPU=1 の方がわずかに高い（58.2 vs 54.8）。小さな操作の並列化オーバーヘッドの累積が FPS に影響

これは、処理するデータサイズに関係なく一律に全ワーカーを使う static 戦略の限界を示している。小さなデータでは goroutine の起動・同期コストが処理コストを上回り、逆効果になる場合がある。

## 要件 (Requirements)

### 必須要件

1. **戦略の切り替え**: VRAM の並列化戦略を CLI フラグで選択できるようにする
   - `-vram-strategy static`: 現在の固定割り当て方式を維持
   - `-vram-strategy dynamic`: 新しい動的割り当て方式を使用
   - デフォルト: `dynamic`

2. **動的ワーカー割り当て (dynamic 戦略)**:
   - 各操作（`clear_vram`, `blit_rect`, `blit_rect_transform`, `copy_rect`）の呼び出し時に、処理対象のデータサイズ（行数 × 幅 = ピクセル数）に基づいて使用する goroutine 数を決定する
   - `-cpu N` は利用可能な最大 goroutine 数の上限として機能する

3. **閾値ベースの判定**:
   - 各コマンドカテゴリごとに「並列化閾値（threshold）」を保持する
   - 処理データのピクセル数が閾値未満の場合 → 1 goroutine（シングルスレッド実行）
   - 処理データのピクセル数が閾値以上の場合 → 最大 goroutine 数で並列実行
   - 初期閾値は合理的なデフォルト値を設定する

4. **パフォーマンスフィードバックによる閾値の自動調整**:
   - 実行時間の計測結果をもとに、閾値を増減させる
   - 並列実行時に期待ほどの高速化が得られなかった場合（オーバーヘッドが支配的）→ 閾値を引き上げる（より大きなデータでなければ並列化しない）
   - 並列実行で十分な高速化が得られた場合 → 閾値を引き下げる（より小さなデータでも並列化を試みる）

5. **static 戦略の保持**: 既存の動作（全操作で常に全ワーカーを使う）を `-vram-strategy static` で選択可能にする。internal な実装変更は最小限に留める

### 任意要件

6. **段階的な goroutine 数**: 閾値を超えた場合に即座に全ワーカーを使うのではなく、データサイズに応じて 2, 4, 8... と段階的に goroutine 数を増やすことも将来的に検討できる構造にする

## 実現方針 (Implementation Approach)

### アーキテクチャ概要

```
VRAMModule
├── Config { Workers, Strategy }
├── workerPool (既存: goroutine プール)
└── dispatcher (新規: 戦略に応じたディスパッチ)
    ├── staticDispatcher  → 常に全ワーカーを使用
    └── dynamicDispatcher → サイズと計測値に基づいて判定
        ├── thresholds map[string]*threshold  (コマンド別閾値)
        └── フィードバックループ
```

### 主要コンポーネント

#### 1. Strategy インタフェース

```go
type dispatchStrategy interface {
    // Decide returns the number of workers to use for the given data size.
    Decide(command string, dataSize int) int
    // Feedback records the performance result for future decisions.
    Feedback(command string, dataSize int, workers int, elapsed time.Duration)
}
```

#### 2. staticDispatcher

既存の動作をそのまま再現。`Decide()` は常に `pool.size` を返す。`Feedback()` は no-op。

#### 3. dynamicDispatcher

- コマンド別の閾値マップを保持
- `Decide(command, dataSize)`: `dataSize < threshold[command]` なら 1、それ以外は `maxWorkers` を返す
- `Feedback(command, dataSize, workers, elapsed)`: 結果を蓄積し、定期的に閾値を調整

#### 4. 閾値調整ロジック

閾値調整は以下の指標で行う:

- **効率比率 (efficiency)**: `singleEstimate / parallelActual`
  - `singleEstimate` = 同サイズでの直近のシングル実行時間（指数移動平均）
  - `parallelActual` = 今回の並列実行時間
- 効率比率が低い（例: < 1.5）場合 → 並列化のメリットが薄い → 閾値を引き上げ
- 効率比率が高い（例: > 2.0）場合 → 並列化が効果的 → 閾値を引き下げ

閾値の変動は急激な変化を避けるため、指数移動平均的な緩やかな調整とする。

#### 5. parallelRows の変更

`parallelRows()` を拡張して、戦略に問い合わせる:

```go
func (v *VRAMModule) parallelRows(command string, totalRows int, fn func(startRow, endRow int)) {
    dataSize := totalRows * pageWidth
    workers := v.strategy.Decide(command, dataSize)

    start := time.Now()
    // 実行 (workers=1 なら直接呼び出し、それ以外はプールにディスパッチ)
    elapsed := time.Since(start)

    v.strategy.Feedback(command, dataSize, workers, elapsed)
}
```

呼び出し側は command 名を渡すようになる:

```go
// 変更前
v.parallelRows(pg.height, func(startRow, endRow int) { ... })
// 変更後
v.parallelRows("clear_vram", pg.height, func(startRow, endRow int) { ... })
```

#### 6. Config の拡張

```go
type Config struct {
    Workers  int    // 1-256, default 1
    Strategy string // "static" or "dynamic", default "dynamic"
}
```

#### 7. CLI フラグの追加

```go
vramStrategy := flag.String("vram-strategy", "dynamic", "VRAM parallelization strategy: static or dynamic")
vramMod := vram.New(vram.Config{Workers: *cpuWorkers, Strategy: *vramStrategy})
```

### 初期閾値のデフォルト値

計測データと一般的なピクセル操作のコスト感から、以下を初期値とする:

| コマンド | 初期閾値 (ピクセル数) | 根拠 |
|:---|---:|:---|
| clear_vram | 4,096 (64x64) | 大きなデータ量で顕著な効果があるため低い閾値 |
| blit_rect | 16,384 (128x128) | 中程度以上のサイズで効果 |
| blit_rect_transform | 32,768 (256x128) | オーバーヘッドが大きいため高い閾値 |
| copy_rect | 8,192 (128x64) | clear に近い特性 |
| その他 | 16,384 | デフォルト値 |

## 検証シナリオ (Verification Scenarios)

### シナリオ 1: static 戦略での後方互換性

1. `-cpu 4 -vram-strategy static` で neurom を起動する
2. stats を取得する
3. 全操作が CPU=4 の従来通りの挙動（全ワーカー使用）であることを確認する

### シナリオ 2: dynamic 戦略の基本動作

1. `-cpu 4 -vram-strategy dynamic` で neurom を起動する
2. 小さなピクセル操作（例: 1ピクセルの draw_pixel）が goroutine=1 で実行されることを確認する
3. 大きなピクセル操作（例: clear_vram の全画面クリア）が複数 goroutine で実行されることを確認する

### シナリオ 3: フィードバックによる閾値調整

1. `-cpu 4 -vram-strategy dynamic` で neurom を起動する
2. 一定期間の実行後、閾値が初期値から変化していることを確認する
3. 小さな blit_rect が並列化されず、大きな clear_vram が並列化される傾向が維持されることを確認する

### シナリオ 4: デフォルト戦略

1. `-cpu 4`（`-vram-strategy` 指定なし）で neurom を起動する
2. dynamic 戦略が適用されていることを確認する

## テスト項目 (Testing for the Requirements)

### 単体テスト

| 要件 | テスト内容 | テストファイル |
|:---|:---|:---|
| 要件1 | Config に Strategy フィールドが正しく設定され、対応する dispatchStrategy が生成されること | `vram_test.go` |
| 要件2 | dynamicDispatcher.Decide() がデータサイズに応じて正しいワーカー数を返すこと | `dispatcher_test.go` (新規) |
| 要件3 | 閾値未満のデータサイズで workers=1、閾値以上で workers=maxWorkers が返ること | `dispatcher_test.go` |
| 要件4 | Feedback() の呼び出しにより閾値が期待通りに増減すること | `dispatcher_test.go` |
| 要件5 | staticDispatcher.Decide() が常に maxWorkers を返すこと | `dispatcher_test.go` |

### 統合テスト

| 要件 | テスト内容 | テストファイル |
|:---|:---|:---|
| 要件1 | CLI フラグ `-vram-strategy static` / `dynamic` が正しく反映されること | `integration/` |
| 要件2 | dynamic 戦略で実際にコマンド実行のパフォーマンスが改善（または維持）されること | パフォーマンス計測（手動確認 + stats CLI） |

### 検証コマンド

```bash
# 全体ビルド + 単体テスト
./scripts/process/build.sh

# 統合テスト
./scripts/process/integration_test.sh
```
