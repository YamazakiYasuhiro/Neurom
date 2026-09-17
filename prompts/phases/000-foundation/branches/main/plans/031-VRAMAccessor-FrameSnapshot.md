# 031-VRAMAccessor-FrameSnapshot

> **Source Specification**: [031-VRAMAccessor-FrameSnapshot.md](../ideas/031-VRAMAccessor-FrameSnapshot.md)

## Goal Description

`VRAMModule` の 7 つの読み出しアクセサは、`handleMessage` が `mu` を保持して書き込む
状態を**ロックなしで**読んでいる。モニタは描画経路でこのアクセサを呼ぶため、
描画ゴルーチンと VRAM の書き込みが常に競合している。
レースディテクタは統合テストでこれを実際に検出する。

7 アクセサを廃止し、表示に必要な状態を読み取りロックの内側で
呼び出し側のバッファへ一括複製する `Snapshot` に置き換える。
これにより競合を除去し、あわせて寸法とバッファが別々の時点から来る
（`set_page_size` を跨いだ範囲外アクセスの危険）問題も解消する。

### 前提として確認済みの事実

実装方針の妥当性は以下の 2 点の確認に依存しており、いずれも調査済みである。

1. **`mu` は全ピクセル書き込みを実際に保護している。**
   `parallelRows` はワーカーへタスクを投げた後 `wg.Wait()` で待つ
   （`vram.go` 225-231 行）。したがってワーカーの書き込みは
   `handleBlitRect` の復帰前に完了し、`handleMessage` が `mu` を解放する前に終わる。
   ワーカーが `handleMessage` より長生きすることはないため、
   **読み取りロックを足すだけで競合は消える**
2. **`RWMutex` の読み側は製品コードで未使用である。**
   `RLock` の出現は `vram_test.go` のデッドロック検査のみであり、
   本変更が既存のロック順序に影響しない

## User Review Required

以下 2 点は実装方針の判断を含むため、着手前に確認いただきたい。

### 1. `Frame` 型の配置

`monitor` は現在 `VRAMAccessor` インターフェースを自前で宣言し、
`vram` パッケージへの import を避けている。
`Snapshot(dst *vram.Frame)` をインターフェースに含めると、
Go の型システム上、`monitor` は `vram` を import せざるを得ない。

本計画は **`Frame` を `vram` パッケージに定義し、`monitor` が `vram` を import する**方針を採る。

- 理由: `monitor` は既に `directColorMarker` の値とパレットのレイアウトという
  `vram` の知識を複製して持っており（`monitor.go` 21 行）、
  依存は構造的インターフェースの裏に隠れていただけで実在する。
  明示的な import にした方が実態に近い
- 却下した案: `Frame` だけを持つ中立パッケージ（例 `internal/vramframe`）を新設する。
  構造体 1 個のためにパッケージを作る割に得るものが少ない。
  なお仕様 027（ARC SDK の embedded frame API）が将来この型を
  `internal/` の外へ移す可能性があり、その際は機械的な移動で済む
- 循環参照は発生しない（`vram` は `monitor` を import していない）

### 2. 検証で `go test -race` を直接実行すること

`planning-rules` §3.1 は実装計画で生の `go test` を書くことを禁じ、
`scripts/process/` のスクリプトを使うよう求めている。
しかし**本計画の主目的である `-race` を、現状のスクリプトは表現できない**。
加えて `integration_test.sh` は存在しない `tests/go.mod` を前提とするため
何も実行しない（仕様 030 の R1）。

したがって競合の検証に限り `go test -race` の直接実行を用いる。
仕様 030 の実装後は `integration_test.sh --race` に置き換える。
この逸脱は Verification Plan にも明記する。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: 描画に必要な状態を単一の呼び出しで一貫して取得できること | Proposed Changes > `vram/frame.go` の `Frame` と `Snapshot` |
| R2: 読んでいる間に書き込まれても競合しないこと | `Snapshot` が `mu.RLock` の内側で複製する。呼び出し側は自分のバッファを読む |
| R3: `-race` が PASS すること | Verification Plan > 3. レースディテクタによる検証 |
| R4: ロックを保持したまま呼び出し側に制御を返さないこと | `Snapshot` は `defer v.mu.RUnlock()` で復帰時に解放する。コールバック形式（案 B）は採らない |
| R5: 描画結果が変わらないこと | `monitor_test.go` の既存 2 テストを新 API で維持。Verification Plan > 4. 実行時の不変性確認 |
| R6: 統合テストが新 API を使い、寸法の決め打ちを廃すること | Proposed Changes > 統合テスト 3 ファイル |
| R7（任意）: フレームあたりのコピーを 1 回以下に抑えること | `resizeTo` によるバッファ再利用と `BenchmarkSnapshot` |
| R8（任意）: 旧 7 アクセサを削除すること | Proposed Changes > `vram.go` から削除。全呼び出し元を調査済み（下記） |

### R8 の対象となる全呼び出し元（調査済み）

削除にあたり修正が必要な箇所は以下で網羅されている。

| ファイル | 箇所 |
| :--- | :--- |
| `internal/modules/vram/vram.go` | 138-146 行（定義そのもの） |
| `internal/modules/monitor/monitor.go` | 25-31 行（インターフェース）、426-430 行（`refreshFromVRAM`） |
| `internal/modules/monitor/monitor_test.go` | 158-164 行（`mockVRAMAccessor`） |
| `internal/modules/vram/vram_test.go` | 854 行（`ViewportOffset`） |
| `integration/vram_enhancement_test.go` | 175 行 |
| `integration/vram_page_test.go` | 195-199 行 |
| `integration/vram_multicore_test.go` | 104-105 行、155-156 行 |

## Proposed Changes

### vram パッケージ

#### [NEW] [features/neurom/internal/modules/vram/frame_test.go](features/neurom/internal/modules/vram/frame_test.go)

*   **Description**: `Snapshot` と `resizeTo` のテスト。実装より先に書き、失敗を確認する
*   **Technical Design**:

    ```go
    func TestResizeTo(t *testing.T)
    func TestSnapshotCopiesDisplayState(t *testing.T)
    func TestSnapshotReusesBuffers(t *testing.T)
    func TestSnapshotGrowsOnPageEnlarge(t *testing.T)
    func TestSnapshotShrinksOnPageShrink(t *testing.T)
    func TestSnapshotConsistencyUnderPageResize(t *testing.T)
    func TestSnapshotDoesNotBlockWrites(t *testing.T)
    func BenchmarkSnapshot(b *testing.B)
    ```

*   **Logic**:
    *   `TestResizeTo`: テーブル駆動。`(cap, len, n)` の組に対する結果を検証する

        | # | 入力 slice | 要求長 n | 期待 |
        | :--- | :--- | :--- | :--- |
        | 1 | `nil` | 4 | 長さ 4 の新規スライス |
        | 2 | `len 0, cap 8` | 4 | 長さ 4、**同じ配列**（`cap` が 8 のまま） |
        | 3 | `len 8, cap 8` | 4 | 長さ 4、同じ配列 |
        | 4 | `len 4, cap 4` | 8 | 長さ 8、新規配列（`cap >= 8`） |
        | 5 | `len 4, cap 4` | 0 | 長さ 0、同じ配列 |

        「同じ配列か」は `cap` の一致ではなく
        `len(b) > 0 && len(got) > 0` のときに `&b[0] == &got[0]` で判定する
    *   `TestSnapshotCopiesDisplayState`: `vram.New()` した直後に
        パレットの 1 エントリ、`index` の 1 バイト、`color` の 4 バイト、
        `viewportX/Y`、`displayPage` を内部から直接書き換え、
        `Snapshot` の結果が 8 フィールドすべて一致することを検証する。
        `Palette` は値のコピーなので、取得後に元を変更しても
        スナップショットが変わらないことも確認する
    *   `TestSnapshotReusesBuffers`: 同じ `*Frame` に対して 2 回 `Snapshot` を呼び、
        1 回目と 2 回目で `&f.Index[0]` および `&f.Color[0]` が
        同一であることを検証する（R7 の中核）
    *   `TestSnapshotGrowsOnPageEnlarge`: 既定 256x212 で 1 回取得した後、
        `set_page_size` で 512x512 に拡大してから再取得し、
        `len(f.Index) == 512*512` かつ `f.Width == 512` になることを検証する
    *   `TestSnapshotShrinksOnPageShrink`: 512x512 の後に 64x64 へ縮小し、
        `len(f.Index) == 64*64` になること（前回の長い長さが残らないこと）を検証する。
        **これが残ると `refreshFromVRAM` が範囲外を読む**ため、
        `resizeTo` が `b[:n]` で必ず切り詰めることの回帰テストになる
    *   `TestSnapshotConsistencyUnderPageResize`: 仕様の検証シナリオ 2 に対応する。
        VRAM を起動し、`set_page_size` を 256x212 と 512x512 の間で
        連続して発行するゴルーチンと、`Snapshot` を繰り返すゴルーチンを並行させる。
        取得したすべての `Frame` について以下の不変条件を検証する
        *   `len(f.Index) >= f.Width * f.Height`
        *   `len(f.Color) >= f.Width * f.Height * 4`
        *   `f.Width > 0 && f.Height > 0`

        タイムアウトはケース A（成功時は即終了）に該当するため 500ms 以内とし、
        反復回数で打ち切る
    *   `TestSnapshotDoesNotBlockWrites`: `Snapshot` が復帰後にロックを
        保持していないこと（R4）を検証する。`Snapshot` を呼んだ後に
        `draw_pixel` を publish し、100ms 以内に反映されることを確認する。
        コールバック方式ならここでブロックし得るという対比を担保する
    *   `BenchmarkSnapshot`: 同じ `*Frame` を再利用して `Snapshot` を反復し、
        `b.ReportAllocs()` で定常状態のアロケーションが 0 であることを示す

#### [NEW] [features/neurom/internal/modules/vram/frame.go](features/neurom/internal/modules/vram/frame.go)

*   **Description**: フレームスナップショットの型と取得関数
*   **Technical Design**:

    ```go
    package vram

    // Frame is a consistent snapshot of the VRAM display state. Every field is
    // read under a single lock acquisition, so the dimensions always describe the
    // buffers they arrive with. The buffers belong to the caller, which is what
    // makes them safe to read while the VRAM module keeps drawing.
    type Frame struct {
        Index   []uint8
        Color   []uint8
        Width   int
        Height  int
        Palette [256][4]uint8
        Page    int
        ViewX   int16
        ViewY   int16
    }

    // Snapshot copies the display state into dst, reusing dst's buffers when they
    // are large enough. Callers are expected to keep one Frame and pass it every
    // time so that a steady state allocates nothing.
    func (v *VRAMModule) Snapshot(dst *Frame)

    // resizeTo returns a slice of length n, keeping b's array when it has room.
    func resizeTo(b []uint8, n int) []uint8
    ```

*   **Logic**:
    *   `Snapshot`:
        1.  `v.mu.RLock()` し、`defer v.mu.RUnlock()` する
        2.  `pg := &v.pages[v.displayPage]` を取る
        3.  `dst.Index = resizeTo(dst.Index, len(pg.index))` の後 `copy(dst.Index, pg.index)`
        4.  `dst.Color = resizeTo(dst.Color, len(pg.color))` の後 `copy(dst.Color, pg.color)`
        5.  `dst.Width = pg.width`、`dst.Height = pg.height`
        6.  `dst.Palette = v.palette`（配列なので値コピー）
        7.  `dst.Page = v.displayPage`、`dst.ViewX = v.viewportX`、`dst.ViewY = v.viewportY`
    *   `resizeTo`:
        *   `if cap(b) >= n { return b[:n] }`
        *   `return make([]uint8, n)`
        *   `b[:n]` で切り詰めるため、ページ縮小時に前回の長さが残らない

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](features/neurom/internal/modules/vram/vram.go)

*   **Description**: 競合の原因である 7 アクセサを削除する
*   **Logic**:
    *   138-146 行の `VRAMBuffer` / `VRAMColorBuffer` / `VRAMWidth` / `VRAMHeight` /
        `VRAMPalette` / `DisplayPage` / `ViewportOffset` と
        直前の `// --- VRAMAccessor methods ---` コメントを削除する
    *   残すと競合が再び持ち込まれる経路が開いたままになるため、
        非推奨コメントを付けて残す選択は採らない

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: 854 行の `ViewportOffset()` を `Snapshot` に置換する
*   **Logic**:
    *   `vpX, vpY := v.ViewportOffset()` を
        `var f Frame; v.Snapshot(&f)` とし、以降 `f.ViewX` / `f.ViewY` を参照する

### monitor パッケージ

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor_test.go](features/neurom/internal/modules/monitor/monitor_test.go)

*   **Description**: モックを `Snapshot` 実装へ置き換える。実装より先に修正する
*   **Technical Design**:

    ```go
    type mockVRAMAccessor struct {
        index   []uint8
        color   []uint8
        width   int
        height  int
        palette [256][4]uint8
        dpg     int
        vpX     int16
        vpY     int16
    }

    func (m *mockVRAMAccessor) Snapshot(dst *vram.Frame) {
        dst.Index = append(dst.Index[:0], m.index...)
        dst.Color = append(dst.Color[:0], m.color...)
        dst.Width, dst.Height = m.width, m.height
        dst.Palette = m.palette
        dst.Page = m.dpg
        dst.ViewX, dst.ViewY = m.vpX, m.vpY
    }
    ```

*   **Logic**:
    *   158-164 行の 7 メソッドを上記 1 メソッドへ置換する
    *   フィールド構成と既存 2 テスト（パレット経路と direct color 経路）の
        検証内容は変更しない。R5（描画結果の不変）の担保がこの 2 テストである

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: インターフェースと `refreshFromVRAM` をスナップショット方式へ移行する
*   **Technical Design**:

    ```go
    import "github.com/axsh/neurom/internal/modules/vram"

    // VRAMAccessor hands over a consistent copy of the VRAM display state.
    // It deliberately does not expose the buffers directly: the VRAM module
    // keeps writing to them while the monitor renders.
    type VRAMAccessor interface {
        Snapshot(dst *vram.Frame)
    }

    type MonitorModule struct {
        // ... existing fields ...
        frame vram.Frame // reused across refreshes; guarded by mu like rgba
    }
    ```

*   **Logic**:
    *   23-32 行のインターフェースを 1 メソッドに置き換える
    *   `MonitorModule` に `frame vram.Frame` を追加する。
        `buildFrame` は常に `m.mu` の下で呼ばれる（363-366 行、465-468 行）ため、
        フィールドとして再利用しても競合しない
    *   `refreshFromVRAM` の先頭 5 行（426-430 行）を差し替える

        ```go
        // Before
        index := v.VRAMBuffer()
        color := v.VRAMColorBuffer()
        pal := v.VRAMPalette()
        vpX, vpY := v.ViewportOffset()
        vw, vh := v.VRAMWidth(), v.VRAMHeight()

        // After
        m.vramAccessor.Snapshot(&m.frame)
        f := &m.frame
        index, color, pal := f.Index, f.Color, f.Palette
        vpX, vpY := f.ViewX, f.ViewY
        vw, vh := f.Width, f.Height
        ```

    *   432 行以降のループ本体は**一切変更しない**。
        `vw` / `vh` による境界判定と `directColorMarker` の分岐、
        `pal[0]` による範囲外の塗り潰しをそのまま維持することが R5 の条件である
    *   ローカル変数名を維持するのは差分を最小化し、
        描画ロジックが変わっていないことをレビューで確認しやすくするためである

### 統合テスト

#### [MODIFY] [features/neurom/integration/vram_multicore_test.go](features/neurom/integration/vram_multicore_test.go)

*   **Description**: 104-105 行と 155-156 行のカラーバッファ比較を `Snapshot` 経由にする
*   **Logic**:
    *   `buf1 := vram1.VRAMColorBuffer()` を
        `var f1, f4 vram.Frame; vram1.Snapshot(&f1); vram4.Snapshot(&f4)` に置き換え、
        以降 `f1.Color` / `f4.Color` を比較する
    *   比較範囲は両者の `len` が一致することを先に確認してから走査する。
        現状は `for i := range buf1` で片側の長さに依存している
    *   このテストが競合検出の発生源（`vram_multicore_test.go:158`）であり、
        修正後にここが PASS することが R3 の直接の証拠になる

#### [MODIFY] [features/neurom/integration/vram_page_test.go](features/neurom/integration/vram_page_test.go)

*   **Description**: 195-201 行の寸法決め打ちを廃す（R6 の中核）
*   **Logic**:
    *   `vramMod.VRAMWidth()` / `VRAMHeight()` / `VRAMBuffer()` の 3 回の呼び出しを
        1 回の `Snapshot` に置き換える

        ```go
        var f vram.Frame
        vramMod.Snapshot(&f)
        if f.Width != 512 || f.Height != 512 {
            t.Errorf("page 0 size = %dx%d, want 512x512", f.Width, f.Height)
        }
        if got := f.Index[400*f.Width+400]; got != 5 {
            t.Errorf("pixel at (400,400) = %d, want 5", got)
        }
        ```

    *   添字は `400*512+400` ではなく `400*f.Width+400` とする。
        取得した寸法で計算することが、寸法とバッファが同一時点であることの利用にあたる

#### [MODIFY] [features/neurom/integration/vram_enhancement_test.go](features/neurom/integration/vram_enhancement_test.go)

*   **Description**: 175 行の `VRAMColorBuffer()` を `Snapshot` 経由にする
*   **Logic**:
    *   `cb := vramMod.VRAMColorBuffer()` を
        `var f vram.Frame; vramMod.Snapshot(&f); cb := f.Color` とし、
        以降の参照は変更しない

## Step-by-Step Implementation Guide

1.  **テストを先に書き、失敗を確認する（TDD red）**:
    *   `features/neurom/internal/modules/vram/frame_test.go` を新規作成し、
        上記 7 テストと 1 ベンチマークを記述する。
    *   `features/neurom/internal/modules/monitor/monitor_test.go` の
        `mockVRAMAccessor` を `Snapshot` 実装に置換する。
    *   `./scripts/process/build.sh` を実行し、`vram` と `monitor` が
        コンパイルエラーで失敗することを確認する。
        **この段階で失敗しなければテストが対象を捉えていない**ので見直す。

2.  **`Frame` と `Snapshot` を実装する（green）**:
    *   `features/neurom/internal/modules/vram/frame.go` を新規作成し、
        `Frame`、`Snapshot`、`resizeTo` を記述する。
    *   `./scripts/process/build.sh` を実行し、`frame_test.go` の
        7 テストが PASS することを確認する（`monitor` はまだ失敗する）。

3.  **`monitor` をスナップショット方式へ移行する**:
    *   `monitor.go` の `VRAMAccessor` インターフェースを
        `Snapshot(dst *vram.Frame)` の 1 メソッドに変更し、
        `github.com/axsh/neurom/internal/modules/vram` を import する。
    *   `MonitorModule` に `frame vram.Frame` フィールドを追加する。
    *   `refreshFromVRAM` の先頭 5 行を `Snapshot` 呼び出しに差し替える。
        **432 行以降のループ本体は変更しない。**
    *   `./scripts/process/build.sh` を実行し、`monitor` の既存 2 テストが
        PASS することを確認する。

4.  **旧アクセサを削除する**:
    *   `vram.go` 138-146 行の 7 メソッドと直前のコメントを削除する。
    *   `vram_test.go` 854 行の `ViewportOffset()` を `Snapshot` に置換する。
    *   `./scripts/process/build.sh` を実行する。
        統合テストが未修正のため `integration` がコンパイルエラーになるのが正しい。

5.  **統合テストを新 API へ移行する**:
    *   `vram_multicore_test.go` の 4 箇所を `Snapshot` 経由にする。
    *   `vram_page_test.go` の寸法決め打ちを `f.Width` ベースに直す。
    *   `vram_enhancement_test.go` の 1 箇所を `Snapshot` 経由にする。
    *   `./scripts/process/build.sh` を実行し、全体が PASS することを確認する。

6.  **削除の完全性を確認する**:
    *   旧アクセサ名が 1 件も残っていないことを検索で確認する。

        ```bash
        grep -rn "VRAMBuffer()\|VRAMColorBuffer()\|VRAMWidth()\|VRAMHeight()\|VRAMPalette()\|ViewportOffset()" features/
        ```

    *   寸法の決め打ちが残っていないことを確認する（R6）。

        ```bash
        grep -rn "512+\|\*512" features/neurom/integration/
        ```

7.  **Verification Plan を実行する**:
    *   下記 Verification Plan の 1 から 5 をこの順に実施し、
        §12 の総合判定を計画書へ追記する。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:

    ```bash
    ./scripts/process/build.sh
    ```

    *   **Log Verification**: `frame_test.go` の 7 テストが `--- PASS` であること。
        `monitor` の既存 2 テスト（パレット経路と direct color 経路）が
        PASS しており、`SKIP` が 0 件であること。

2.  **Integration Tests**:

    ```bash
    ./scripts/process/build.sh && ./scripts/process/integration_test.sh --specify "TestMultiCoreBlendModes|TestPageSizeIntegration|TestVRAMAccessorMonitorIntegration"
    ```

    > [!WARNING]
    > **このコマンドは現時点では何も実行しない。**
    > `integration_test.sh` は存在しない `tests/go.mod` を前提としており、
    > 警告を出して終了コード 0 で成功する（仕様 030 の R1）。
    > 仕様 030 の実装後に初めて有効になる。
    > それまでは手順 3 のコマンドが統合テストの実行を兼ねる。

3.  **レースディテクタによる検証（本計画の主目的）**:

    ```bash
    cd features/neurom && go test -race -count=3 ./integration/ ./internal/...
    ```

    > [!NOTE]
    > `planning-rules` §3.1 は生の `go test` を禁じているが、
    > `-race` を表現できるスクリプトが存在せず、
    > `integration_test.sh` 自体が無効であるため、本項目のみ例外とする
    > （User Review Required の 2 を参照）。
    > 仕様 030 の実装後に `./scripts/process/integration_test.sh --race` へ置き換える。

    *   **Log Verification**: 出力に `WARNING: DATA RACE` が 1 件も無いこと。
        `race detected during execution of test` が無いこと。
    *   `-count=3` とするのは、競合の検出が実行タイミングに依存するためである。
        修正前は `TestMultiCoreBlendModes/Alpha` と `TestPageSizeIntegration` で
        必ず検出されるので、この 2 件が PASS することが直接の証拠になる。
    *   所要時間の目安は約 55 秒（`-count=1` 時の実測値）である。

4.  **実行時の不変性確認（R5）**:

    ```bash
    ./bin/neurom.exe --headless --stats-port 18099
    ./bin/stats.exe --endpoint http://127.0.0.1:18099/stats
    ```

    *   **Log Verification**: 計上されているコマンド名の集合が修正前と一致すること。
        `tmp/neurom_run.log` に `panic` および `recovered` が無いこと。
    *   ポートが使用中の場合は `bind: Only one usage of each socket address`
        がログに出るため、別ポートで再実行する。
    *   ウィンドウ表示での起動も行い、7 シーンのデモが修正前と同じ見た目で
        動作することを確認する。**描画の見た目のみ目視とする**
        （`testing-rules` §3.3 の例外規定に該当する。
        機能的な検証は手順 1 から 3 で自動化済みである）。

5.  **ベンチマーク（R7、任意）**:

    ```bash
    cd features/neurom && go test -bench BenchmarkSnapshot -benchmem ./internal/modules/vram/
    ```

    *   **Log Verification**: `allocs/op` が 0 であること。
        1 以上なら `resizeTo` がバッファを再利用できていない。

### E2E Tests

本計画では E2E テストを追加しない。理由は以下である。

*   本変更は**内部 API の置換であり、外部から観測可能な振る舞いを変えない**。
    バス上のプロトコル、コマンド、イベント、統計の出力はいずれも変更しない
*   新規に検証すべき対象は「並行アクセス時に競合しないこと」であり、
    これは E2E ではなくレースディテクタ（手順 3）でのみ検出できる
*   `features/neurom/integration/` の既存統合テストが
    バス経由でのコマンド発行と結果の読み出しという経路を既に覆っており、
    それらを新 API へ移行すること（Proposed Changes）が実質的な結合検証になる

### テスト項目設計のセルフレビュー（testing-rules §11.4）

#### ボトムアップの確認順序（§11.2）

依存関係は `refreshFromVRAM → Snapshot → resizeTo` である。
テストもこの逆順に積み上げる。

| Step | 対象 | テスト | 前提とするもの |
| :--- | :--- | :--- | :--- |
| 1 | `resizeTo` | `TestResizeTo` | なし（純関数） |
| 2 | `Snapshot` | `TestSnapshot*` 6 件 | `resizeTo` が動作していること |
| 3 | `refreshFromVRAM` | `monitor` 既存 2 テスト | `Snapshot` が動作していること |
| 4 | バス経由の全体 | 統合テスト 3 ファイル | 上記すべて |

#### 観点チェックリスト（§11.3）

| # | 観点 | 対応するテスト |
| :--- | :--- | :--- |
| 1 | 正常系 | `TestSnapshotCopiesDisplayState`、`monitor` 既存 2 テスト |
| 2 | 異常系・境界値 | `TestResizeTo` の `n=0` と容量不足、`TestSnapshotShrinksOnPageShrink` |
| 3 | 外部連携の実動作 | 統合テスト 3 ファイル（実バス経由）、手順 4 の実行時確認 |
| 4 | データの一貫性 | `TestSnapshotConsistencyUnderPageResize`（不変条件の検証） |
| 5 | 状態遷移 | `TestSnapshotGrowsOnPageEnlarge` / `Shrinks`（`set_page_size` の前後） |
| 6 | 設定・構成の反映 | `TestMultiCoreBlendModes` が `Workers=1` と `Workers=4` の両方を通る |
| 7 | 副作用 | `TestSnapshotDoesNotBlockWrites`（ロックを持ち越さない）、`BenchmarkSnapshot`（アロケーション） |

#### セルフレビューの結果

1.  **網羅性**: この項目群が全て成功すれば、
    「一貫した状態が取得でき、並行書き込み中でも競合せず、描画結果が変わらない」
    と言い切れる。当初 `resizeTo` の切り詰め（ページ縮小）と
    「ロックを持ち越さない」の 2 点が抜けていたため、
    `TestSnapshotShrinksOnPageShrink` と `TestSnapshotDoesNotBlockWrites` を追加した。
2.  **証拠の十分性**: 「エラーが出ない」ではなく、
    複製された値の一致（`TestSnapshotCopiesDisplayState`）、
    ポインタの同一性（`TestSnapshotReusesBuffers`）、
    長さの不変条件（`TestSnapshotConsistencyUnderPageResize`）という
    具体的な値で判定している。
3.  **迂回・抜け道の排除**: 旧 7 アクセサを**削除する**ため、
    テストが偶然古い経路を通って成功する可能性が構造的に無い。
    残した場合はここが抜け道になり得たので、R8 を任意要件ではなく実施対象とした。
4.  **依存関係の整合性**: Step 1 から 4 の順に前段の成立を前提としている。
    ただし手順 2 の `integration_test.sh` は現状無効であり、
    その分の検証は手順 3 の `go test -race` が担う。
    **この 1 点だけ規範どおりの経路になっていない**ことを明記する。

### 総合判定（testing-rules §12）

手順 1 から 5 の完了後、§12.2 の 7 項目を確認し、
§12.3 のフォーマットで判定結果を本計画書の末尾に追記する。

特に本計画で注意して確認すべき項目は以下である。

| # | 項目 | 本計画での着目点 |
| :--- | :--- | :--- |
| 1 | スキップされたテスト | `integration_test.sh` が警告のみで成功する既知の無効化に騙されないこと。**実行されたテスト名を数えて確認する** |
| 2 | 部分的なエラー | `-race` の出力に `WARNING` が混ざっていないか。テスト全体の PASS だけを見ないこと |
| 3 | 迂回処理による偽成功 | 旧アクセサが本当に削除され、`grep` で 0 件であること |
| 6 | カバレッジの妥当性 | `Snapshot` の並行アクセス検証が、単に既存テストの通過で済まされていないこと |

## Documentation

#### [MODIFY] [prompts/specifications/VRAM-Specification.md](prompts/specifications/VRAM-Specification.md)

*   **更新内容**: 「8. 既知の制約」に記載された制約のうち、
    本計画で解消するものを更新する。
    表示状態の読み出しが `Snapshot` 経由の一貫したコピーになったこと、
    および寸法とバッファが同一時点であることが保証されるようになったことを追記する。
    フレーム原子性（描画途中が見えないこと）は仕様 025 の担当であり、
    **本計画では解消しない**旨を明記して取り違えを防ぐ。

#### [MODIFY] [prompts/specifications/ARC-Architecture.md](prompts/specifications/ARC-Architecture.md)

*   **更新内容**: モニタと VRAM の関係について、
    モニタが VRAM のバッファを直接参照するのではなく
    フレームのコピーを受け取る構成であることを記述する。
    仕様 026（vsync）がスナップショットの取得契機になる見込みであることも併記する。
