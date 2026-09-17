# 031: VRAM アクセサのデータ競合修正 — フレームスナップショットの導入

## 背景

仕様 030（テスト基盤整備）の調査でレースディテクタを試行した結果、
**製品コードに実在するデータ競合**を検出した。
テスト固有の問題ではなく、モニタの描画経路そのものに存在する。

### 検出結果

```bash
cd features/neurom && go test -race -count=1 ./integration/
```

```text
WARNING: DATA RACE
Read at 0x00c0002fe004 by goroutine 153:
  integration.TestMultiCoreBlendModes.func1()
      vram_multicore_test.go:158

Previous write at 0x00c0002fe004 by goroutine 154:
  vram.(*VRAMModule).handleBlitRect.func1()
      vram.go:362
  vram.(*VRAMModule).parallelRows()
  vram.(*VRAMModule).handleBlitRect()
  vram.(*VRAMModule).handleMessage()
  vram.(*VRAMModule).run()
```

`--- FAIL: TestMultiCoreBlendModes` および `--- FAIL: TestPageSizeIntegration` として
`race detected during execution of test` で失敗する。

### 原因: 読み出し側がロックを取っていない

`VRAMModule` は `mu sync.RWMutex` を保持し、
`handleMessage` は書き込み中に `v.mu.Lock()` を取得している（`vram.go` 162-163 行）。

一方、`VRAMAccessor` の 7 メソッドは**一切ロックを取らずに**同じ状態を読む
（`vram.go` 138-146 行）。

```go
func (v *VRAMModule) VRAMBuffer() []uint8        { return v.pages[v.displayPage].index }
func (v *VRAMModule) VRAMColorBuffer() []uint8   { return v.pages[v.displayPage].color }
func (v *VRAMModule) VRAMWidth() int             { return v.pages[v.displayPage].width }
func (v *VRAMModule) VRAMHeight() int            { return v.pages[v.displayPage].height }
func (v *VRAMModule) VRAMPalette() [256][4]uint8 { return v.palette }
func (v *VRAMModule) DisplayPage() int           { return v.displayPage }
func (v *VRAMModule) ViewportOffset() (int16, int16) { return v.viewportX, v.viewportY }
```

`RWMutex` の読み側は**製品コードで一度も使われていない**
（`RLock` の出現箇所は `vram_test.go` のデッドロック検査のみ）。

### これは製品の不具合である

モニタは描画経路でこれらのアクセサを呼んでいる（`monitor.go` 426-430 行）。

```go
index := v.VRAMBuffer()
color := v.VRAMColorBuffer()
pal := v.VRAMPalette()
vpX, vpY := v.ViewportOffset()
vw, vh := v.VRAMWidth(), v.VRAMHeight()
```

モニタの描画ゴルーチンが、VRAM モジュールの書き込みゴルーチンおよび
ワーカープール（`Workers > 1` の場合）と**並行して同じ配列を読んでいる**。
テストが競合を顕在化させただけで、競合そのものは実行時に常に存在する。

### 単にロックを足すだけでは直らない

`VRAMBuffer()` の内部で `RLock` を取っても解決しない。
**スライスを返した時点でロックは解放され、呼び出し側は解放後に中身を読む**ためである。
返り値がスライス（参照）である限り、アクセサ単体のロックは無意味である。

### さらに深刻な問題: 状態が引き裂かれる

寸法とバッファを**別々の呼び出しで取得**しているため、
データ競合を無視しても論理的な不整合が起こり得る。

`set_page_size` が 2 つの呼び出しの間に処理されると、
モニタは古い寸法で新しいバッファに添字アクセスする。
`w × h` が縮小した場合は**範囲外アクセスによるパニック**になる。

統合テストにも同じ危険な形が存在する（`vram_page_test.go` 195-201 行）。

```go
if vramMod.VRAMWidth() != 512 || vramMod.VRAMHeight() != 512 { ... }
buf := vramMod.VRAMBuffer()
if buf[400*512+400] != 5 { ... }   // 512 を決め打ちで添字計算している
```

したがって必要なのは個別アクセサへのロック追加ではなく、
**一貫した状態を一度に受け渡す仕組み**である。

## 要件

### 必須要件

| ID | 要件 |
| :--- | :--- |
| R1 | 描画に必要な状態（インデックスバッファ、カラーバッファ、幅、高さ、パレット、表示ページ、ビューポートオフセット）を**単一の呼び出しで一貫して取得**できること。異なる時点の状態が混ざらないこと |
| R2 | 取得した状態を読んでいる間に VRAM モジュールが書き込んでもデータ競合が発生しないこと |
| R3 | `go test -race ./integration/` および `go test -race ./internal/...` が PASS すること |
| R4 | ロックを保持したまま呼び出し側に制御を返さないこと。または保持する場合はその範囲と理由が API 上明示されていること |
| R5 | 現行の描画結果が変わらないこと。モニタの出力とヘッドレス時の統計に差異が出ないこと |
| R6 | 統合テストが新 API を用いて状態を読むこと。寸法の決め打ち（`400*512+400`）を廃し、取得した寸法を用いること |

### 任意要件

| ID | 要件 | 備考 |
| :--- | :--- | :--- |
| R7 | フレームあたりのコピー回数を 1 回以下に抑えること | 256×212 で約 217KB（インデックス + カラー）。60fps で毎フレーム複製すると帯域が無視できない |
| R8 | 旧 `VRAMAccessor` の 7 メソッドを削除すること | 外部から使われていないことを確認した上で行う |

## 実現方針

### 案 A: スナップショットの受け渡し（推奨）

VRAM 側が書き込みロックの内側で一貫した状態を**呼び出し側の再利用バッファへ複製**する。

```go
// Frame は一貫した表示状態のスナップショットである。
// バッファは呼び出し側が所有するため、VRAM の書き込みと競合しない。
type Frame struct {
    Index    []uint8
    Color    []uint8
    Width    int
    Height   int
    Palette  [256][4]uint8
    Page     int
    ViewX    int16
    ViewY    int16
}

// Snapshot は表示状態を dst に複製する。
// dst のバッファ容量が不足する場合のみ再確保するため、
// 定常状態では追加のアロケーションが発生しない。
func (v *VRAMModule) Snapshot(dst *Frame)
```

- 利点: 呼び出し側はロックを一切意識しない。描画時間が VRAM の書き込みを止めない
- 欠点: フレームあたり 1 回の複製が発生する（R7 の観点）

### 案 B: コールバックによるロック範囲の明示

```go
// WithFrame は読み取りロックを保持したまま fn を呼ぶ。
// fn の実行中は VRAM への書き込みが停止するため、短時間で戻ること。
func (v *VRAMModule) WithFrame(fn func(f Frame))
```

- 利点: 複製が不要
- 欠点: 描画中に VRAM の書き込みが停止する。`fn` が重いと描画コマンドが詰まる

### 判断

**案 A を推奨する。**

理由は、モニタの描画（ウィンドウへの転送）が VRAM の書き込みを止めるべきではないためである。
案 B ではフレーム描画が遅い環境で描画コマンドの処理が停止し、
バスの購読チャネルが溢れて**コマンドが黙って破棄される**（この破棄挙動は仕様 025 の対象）。
複製 1 回のコストの方が、描画と演算が相互にブロックする設計より安全である。

> [!NOTE]
> **設計判断（確定）**: 案 A（スナップショットの複製）を採用する。
> 案 B のコピー不要という利点よりも、描画と VRAM 書き込みが
> 相互にブロックしない性質を優先する。

### 仕様 025 / 026 との関係

- **026（フレームクロックと vsync）**: スナップショットを取る自然な契機は vsync である。
  026 実装後は「vsync のたびに `Snapshot` して描画する」形になり、
  複製の頻度がリフレッシュレートで上限を持つ。本仕様は 026 に先行して
  スナップショット API を用意する位置付けである
- **025（バッチとフレーム原子性）**: 現状は 1 コマンド 1 メッセージであるため、
  スナップショットが描画途中の状態を捉えることがある。
  これは競合ではなく原子性の問題で、025 のバッチ化で解決する。
  本仕様では**競合の解消までを担保し、原子性は扱わない**

## 検証シナリオ

### シナリオ 1: 競合が消えたことの確認

1. `cd features/neurom && go test -race -count=1 ./integration/` を実行する
2. `WARNING: DATA RACE` が出力されないことを確認する
3. `TestMultiCoreBlendModes` と `TestPageSizeIntegration` が PASS することを確認する
4. `go test -race -count=1 ./internal/...` を実行し PASS することを確認する
5. 競合は再現性が低いため `-count=3` でも PASS することを確認する

### シナリオ 2: 状態の一貫性の確認

1. VRAM を起動し、`set_page_size` を連続して発行しながら別ゴルーチンで `Snapshot` を呼ぶテストを書く
2. 取得した `Frame` について `len(Index) >= Width*Height` が常に成立することを確認する
3. `len(Color) >= Width*Height*4` が常に成立することを確認する
4. 範囲外アクセスによるパニックが発生しないことを確認する

### シナリオ 3: 描画結果の不変性の確認

1. 修正前の状態で `./bin/neurom.exe --headless --stats-port 18099` を起動する
2. `./bin/stats.exe --endpoint http://127.0.0.1:18099/stats` で統計を取得し記録する
3. 修正後に同じ手順を実行する
4. 計上されているコマンド名の集合が一致することを確認する
5. ウィンドウ表示で起動し、7 シーンのデモが修正前と同じ見た目で動作することを目視で確認する

### シナリオ 4: 複製コストの確認（R7）

1. `Snapshot` を連続呼び出しするベンチマークを書く
2. `go test -bench` でフレームあたりの所要時間とアロケーション回数を測定する
3. 定常状態でアロケーションが 0 回であること（バッファが再利用されていること）を確認する

## テスト項目

| 要件 | 検証手段 | 実行コマンド |
| :--- | :--- | :--- |
| R1: 一貫した取得 | シナリオ 2 の新規テスト | `go test -run TestSnapshotConsistency ./internal/modules/vram/` |
| R2, R3: 競合の解消 | シナリオ 1 | `go test -race -count=3 ./integration/ ./internal/...` |
| R4: ロック範囲 | コードレビュー。`Snapshot` が戻る前にロックを解放していること | — |
| R5: 描画結果の不変 | シナリオ 3 | ヘッドレス起動 + `/stats` 取得、およびウィンドウ表示の目視 |
| R6: テストの修正 | 寸法決め打ちが残っていないことの検索 | `grep -rn "512+" features/neurom/integration/` |
| R7: 複製コスト（任意） | シナリオ 4 | `go test -bench BenchmarkSnapshot ./internal/modules/vram/` |

### ビルド・全体検証

1. ビルド + 単体テスト:

   ```bash
   scripts/process/build.sh
   ```

2. 統合テスト（VRAM とモニタの描画経路）:

   ```bash
   scripts/process/integration_test.sh --categories "vram,monitor"
   ```

3. レースディテクタによる検証（本仕様の主目的）:

   ```bash
   cd features/neurom && go test -race -count=3 ./integration/ ./internal/...
   ```

> [!NOTE]
> 手順 2 の `--categories` は仕様 030 の実装後に使用可能になる。
> 030 が未実装の段階では `cd features/neurom && go test -count=1 ./integration/` を用いる。

## 関連仕様

| 仕様 | 関係 |
| :--- | :--- |
| 030 | 本問題の発見契機。030 の `-race` ゲート（R8）は本仕様の完了が前提 |
| 026 | vsync がスナップショットを取る自然な契機になる。本仕様の API を利用する |
| 025 | フレーム原子性（描画途中が見えない保証）は 025 の担当。本仕様は競合のみを扱う |
