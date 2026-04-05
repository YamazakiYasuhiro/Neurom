# 014: VRAM処理のマルチコア対応

## 背景 (Background)

現在のVRAMモジュールは、すべての描画処理（`clear_vram`, `blit_rect`, `blit_rect_transform`, `copy_rect` 等）を単一のgoroutine内で逐次実行している。`handleMessage` で `mu.Lock()` を取得した後、ピクセル単位のループを1スレッドで処理するため、大きなページサイズや複雑なブレンド・変換処理ではCPUのマルチコア性能を活かせていない。

特に以下の処理はピクセル数に比例した計算量があり、並列化の恩恵が大きい:

- `clear_vram`: ページ全体のバッファを塗りつぶす
- `blit_rect`: 矩形領域へのピクセル書き込み（ブレンドモード付き）
- `blit_rect_transform`: 回転・スケーリング変換後のピクセル書き込み
- `copy_rect`: 矩形領域のコピー（読み出し→書き込みの2段階）
- `TransformBlit`: 逆変換による出力ピクセル計算（`transform.go`内）

## 要件 (Requirements)

### 必須要件

1. **起動オプション `--cpu N` の追加**
   - `main.go` の `flag` に `--cpu` フラグ（整数型）を追加する
   - `N` はワーカー goroutine の数を指定する（デフォルト: `1`）
   - `N=1` の場合は現在と同じ逐次実行を行い、goroutine プール起動のオーバーヘッドを回避する
   - `N` の有効範囲: `1 ≤ N ≤ 256`（範囲外の場合は `1` にフォールバック）

2. **goroutine ワーカープールの実装**
   - VRAMモジュールの初期化時に `N` 個のワーカーgoroutineを起動する
   - ワーカーはタスクチャネルから作業単位（行範囲）を受け取って処理する
   - VRAMモジュールの `Stop()` 時にプールを適切にシャットダウンする

3. **VRAMループ処理の行分割による並列化**
   - 対象操作の処理対象行を `N` 分割し、各ワーカーに割り当てる
   - 各ワーカーが担当する行範囲は重複しないこと（書き込み競合の回避）
   - すべてのワーカーの完了を `sync.WaitGroup` で待機してからイベントを発行する

4. **並列化対象の操作**
   - `clear_vram`: ページ全体のindex/colorバッファの塗りつぶし
   - `blit_rect`: クリップ後の矩形領域へのピクセル書き込み
   - `blit_rect_transform`: 変換後の矩形領域へのピクセル書き込み
   - `copy_rect`: 一時バッファへの読み出し、および宛先への書き込み
   - `TransformBlit` (`transform.go`): 逆変換ループの並列化

5. **既存動作との互換性**
   - `--cpu 1`（デフォルト）の場合、既存のテストがすべてそのまま通ること
   - マルチコア時も描画結果は逐次実行時と完全に一致すること（ピクセル単位で同一）
   - バスメッセージのイベント発行タイミング・内容は変更しない

### 任意要件

- `runtime.NumCPU()` をデフォルト値として使用する案も検討可能だが、初期実装ではデフォルト `1`（現状維持）とする
- 将来的にバッチ処理（複数メッセージをまとめて処理）と組み合わせる拡張の余地を考慮する

## 実現方針 (Implementation Approach)

### アーキテクチャ概要

```
main.go
  │
  ├── flag.Int("cpu", 1, "...")  ← 起動オプション追加
  │
  └── vram.New(vram.Config{Workers: N})
        │
        ├── workerPool (N goroutines)
        │     ├── worker 0: rows [0, H/N)
        │     ├── worker 1: rows [H/N, 2H/N)
        │     ├── ...
        │     └── worker N-1: rows [(N-1)H/N, H)
        │
        └── handleMessage()
              ├── mu.Lock()
              ├── dispatch(task, rowRange) → workerPool
              ├── wg.Wait()  ← 全ワーカー完了待ち
              ├── publishEvent()
              └── mu.Unlock()
```

### 主要コンポーネント

#### 1. ワーカープール (`workerPool`)

```go
type rowTask struct {
    fn      func(startRow, endRow int)
    startRow int
    endRow   int
    wg       *sync.WaitGroup
}

type workerPool struct {
    tasks chan rowTask
    quit  chan struct{}
    size  int
}
```

- VRAMModule内にワーカープールを保持
- `Start()` 時にgoroutineを起動、`Stop()` 時に `quit` チャネルで終了
- `N=1` の場合はプールを作らず、直接関数を呼び出す

#### 2. 行分割ユーティリティ

```go
func splitRows(totalRows, numWorkers int) [][2]int
```

- 総行数を `numWorkers` 個に均等分割する
- 端数は先頭のチャンクに振り分ける
- 行数がワーカー数未満の場合は、行数分のチャンクのみ返す

#### 3. 並列ディスパッチ

各描画操作で共通の並列ディスパッチパターンを使用:

```go
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

### スレッド安全性の保証

- `handleMessage` は `mu.Lock()` を保持した状態で呼ばれる
- ワーカーgoroutineは行範囲が重複しないため、同一バッファへの書き込み競合は発生しない
- パレット配列 (`v.palette`) は読み取り専用として参照される（書き込みは `set_palette` 等の別操作）
- `mu.Lock()` はすべてのワーカー完了後に解放されるため、外部からのバッファアクセスとの競合もない

### 変更対象ファイル

| ファイル | 変更内容 |
|---------|---------|
| `cmd/main.go` | `--cpu` フラグの追加、`vram.New()` への引数追加 |
| `internal/modules/vram/vram.go` | `Config` 構造体追加、`New(Config)` シグネチャ変更、`workerPool` 統合、各描画操作の並列化 |
| `internal/modules/vram/worker.go` | ワーカープール実装（新規ファイル） |
| `internal/modules/vram/transform.go` | `TransformBlit` の並列化対応 |

## 検証シナリオ (Verification Scenarios)

### シナリオ1: デフォルト動作（`--cpu 1`）

1. `--cpu` フラグなしで起動する
2. 全既存テスト（単体テスト・統合テスト）が通ることを確認する
3. VRAMの描画結果がフラグ導入前と完全に一致することを確認する

### シナリオ2: マルチコア動作（`--cpu 4`）

1. `--cpu 4` で起動する
2. `clear_vram` で 256×212 ページを塗りつぶす
3. 塗りつぶし結果が全ピクセル一致することを確認する
4. `blit_rect` で 100×100 の矩形をページに書き込む
5. 書き込み結果がシングルコア時と一致することを確認する
6. `blit_rect_transform` で回転・スケーリング付きの描画を行う
7. 描画結果がシングルコア時と一致することを確認する

### シナリオ3: 行数がワーカー数より少ない場合

1. `--cpu 8` で起動する
2. 高さ 3 の矩形に対して `blit_rect` を実行する
3. 行数 (3) < ワーカー数 (8) でも正しく処理されることを確認する
4. 高さ 1 の矩形でも動作すること

### シナリオ4: 境界値テスト

1. `--cpu 0` で起動 → `1` にフォールバックし、正常動作すること
2. `--cpu 257` で起動 → `1` にフォールバックし、正常動作すること
3. `--cpu 256` で起動 → 正常動作すること

### シナリオ5: シャットダウン

1. `--cpu 4` で起動する
2. シャットダウンコマンドを送信する
3. ワーカーgoroutineがリークせず、正常終了すること

## テスト項目 (Testing for the Requirements)

### 単体テスト

| テスト | 対象要件 | 検証方法 |
|-------|---------|---------|
| `TestSplitRows` | 行分割ユーティリティ | 様々な行数とワーカー数の組み合わせで分割結果を検証 |
| `TestWorkerPoolStartStop` | ワーカープールのライフサイクル | プールの起動・停止でgoroutineリークがないことを確認 |
| `TestClearVRAMMultiCore` | `clear_vram` 並列化 | workers=1,2,4 で結果が同一であることを比較 |
| `TestBlitRectMultiCore` | `blit_rect` 並列化 | workers=1,2,4 で結果が同一であることを比較 |
| `TestBlitRectTransformMultiCore` | `blit_rect_transform` 並列化 | workers=1,2,4 で結果が同一であることを比較 |
| `TestCopyRectMultiCore` | `copy_rect` 並列化 | workers=1,2,4 で結果が同一であることを比較 |
| `TestTransformBlitParallel` | `TransformBlit` 並列化 | workers=1,2,4 で結果が同一であることを比較 |
| `TestCPUFlagValidation` | フラグ範囲チェック | 0, 1, 4, 256, 257 の各値でフォールバック動作を検証 |

### 統合テスト

| テスト | 対象要件 | 検証方法 |
|-------|---------|---------|
| `TestMultiCoreClearAndBlit` | 描画パイプライン全体 | `--cpu 4` 相当の設定で clear → blit → 結果確認 |
| `TestMultiCoreBlendModes` | ブレンドモードとの併用 | Alpha/Additive/Multiply/Screen の各モードでマルチコア結果を検証 |
| `TestMultiCoreShutdown` | シャットダウン安全性 | マルチコア動作中にシャットダウンしてもハングしないこと |

### 検証コマンド

```bash
# 全体ビルドと単体テスト
scripts/process/build.sh

# 統合テスト
scripts/process/integration_test.sh

# マルチコア関連テストのみ
scripts/process/integration_test.sh --specify TestMultiCore
```
