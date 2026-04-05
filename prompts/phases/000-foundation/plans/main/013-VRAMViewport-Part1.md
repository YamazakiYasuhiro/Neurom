# 013-VRAMViewport-Part1

> **Source Specification**: [prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md](file://prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md)

## Goal Description

VRAMのページ単位サイズ管理とblit系コマンドのsrc_page廃止を実装する。Part 1ではVRAMModule内部の構造変更（R1〜R4, R10）を対象とし、MonitorModule・ビューポート関連はPart 2で扱う。

## User Review Required

- R4 `copy_page` のリサイズ動作: デスティネーションページをソースと同サイズにリサイズしてコピーする仕様が、既存コードの `copy_page` テスト（同サイズ前提）に影響する。テストを更新するが、動作が正しいか確認いただきたい。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: ページ単位のサイズ管理 | Proposed Changes > vram.go (pageBuffer拡張, VRAMModule width/height削除, 全コマンドハンドラ修正) |
| R2: set_page_size バスコマンド | Proposed Changes > vram.go (handleSetPageSize 新規追加) |
| R3: ページ追加時のデフォルトサイズ | Proposed Changes > vram.go (handleSetPageCount 修正) |
| R4: ページ間操作のサイズ差異対応 | Proposed Changes > vram.go (handleCopyPage, handleCopyRect 修正) |
| R10: blit系コマンドからsrc_page廃止 | Proposed Changes > vram.go (handleBlitRect, handleBlitRectTransform ワイヤフォーマット変更) |
| R5〜R9: ビューポート・モニタ | **Part 2 で実装** |

## Proposed Changes

### VRAM Module

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: Part 1 の全変更に対するTDD用単体テストを先に追加・修正する。
*   **Technical Design**:

    **1. pageBuffer サイズ保持テスト (R1)**:
    ```go
    func TestPageBufferHasSize(t *testing.T) {
        // newPageBuffer(w, h) が width/height を保持することを検証
        // New() のデフォルトページが 256×212 であることを検証
    }
    ```

    **2. set_page_size テスト (R2)**:
    ```go
    func TestSetPageSize(t *testing.T) {
        tests := []struct {
            name      string
            page      uint8
            width     uint16
            height    uint16
            expectErr bool
        }{
            {"resize_page0", 0, 512, 424, false},
            {"resize_to_small", 0, 64, 64, false},
            {"invalid_page", 5, 256, 212, true},
        }
        // set_page_size コマンド送信 → page_size_changed イベント検証
        // バッファ長が新サイズと一致することを検証
        // 既存ピクセルがクリアされることを検証
    }
    ```

    **3. 大きいページへの描画テスト (R1)**:
    ```go
    func TestDrawPixelOnLargePage(t *testing.T) {
        // page 0 を 512×424 にリサイズ
        // (300, 300) にピクセルを描画 → 成功
        // (512, 0) にピクセルを描画 → 範囲外で無視
    }
    ```

    **4. set_page_count デフォルトサイズテスト (R3)**:
    ```go
    func TestSetPageCountNewPagesDefaultSize(t *testing.T) {
        // page 0 を 512×424 にリサイズ
        // set_page_count(3) で新ページ追加
        // pages[1], pages[2] が 256×212 であることを検証
        // pages[0] が 512×424 のまま変わらないことを検証
    }
    ```

    **5. copy_page リサイズ動作テスト (R4)**:
    ```go
    func TestCopyPageResizesDestination(t *testing.T) {
        // page 0: 512×424, page 1: 256×212
        // copy_page(0→1): page 1 が 512×424 にリサイズされ、内容がコピーされる
    }
    ```

    **6. copy_rect 異なるサイズ間クリッピングテスト (R4)**:
    ```go
    func TestCopyRectCrossPageDifferentSizes(t *testing.T) {
        // page 0: 512×424, page 1: 128×128
        // copy_rect(page0 (0,0) → page1 (0,0), 200×200)
        // page 1 の範囲 (128×128) にクリップされること
    }
    ```

    **7. blit_rect src_page廃止テスト (R10)**:
    ```go
    func TestBlitRectNewFormat(t *testing.T) {
        // 新フォーマット (10 bytes header) で blit_rect が正しく動作すること
    }
    func TestBlitRectBlendFromDstPage(t *testing.T) {
        // ブレンド時に dst_page の既存ピクセルを背景として使用すること
        // 赤を page 0 に描画 → 青を AlphaBlend で page 0 に上書き
        // dst_page (page 0) の赤がブレンド背景として使われること
    }
    func TestBlitRectTransformNewFormat(t *testing.T) {
        // 新フォーマット (19 bytes header) で blit_rect_transform が正しく動作すること
    }
    ```

    **8. 既存テストのヘルパー関数修正 (R10)**:
    *   `buildBlitRectMsg` / `buildBlitRectMsgPage`: src_page パラメータ削除、オフセット調整
    *   `buildBlitRectTransformMsg` / `buildBlitRectTransformMsgPage`: 同上
    *   全既存テスト内の `v.width` 参照を `v.pages[X].width` に変更

*   **Logic**:
    *   テストケースは TDD に基づき、先に書いて失敗を確認してから実装に進む。
    *   既存テスト (`TestVRAMModule`, `TestBlitRect`, `TestBlitRectWithBlend`, `TestAlphaBlendExact`, `TestBlitRectTransform` 等) のヘルパー関数のワイヤフォーマットを新仕様に修正する。

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: pageBuffer の拡張、VRAMModule の width/height 削除、全コマンドハンドラのリファクタリング。
*   **Technical Design**:

    **pageBuffer 構造体の拡張 (R1)**:
    ```go
    type pageBuffer struct {
        width  int
        height int
        index  []uint8
        color  []uint8
    }

    func newPageBuffer(w, h int) pageBuffer {
        return pageBuffer{
            width:  w,
            height: h,
            index:  make([]uint8, w*h),
            color:  make([]uint8, w*h*4),
        }
    }
    ```

    **VRAMModule 構造体の変更 (R1)**:
    ```go
    type VRAMModule struct {
        mu          sync.RWMutex
        // width, height フィールドを削除
        color       int
        pages       []pageBuffer
        displayPage int
        viewportX   int // Part 2 で使用（ここでは 0 固定）
        viewportY   int // Part 2 で使用（ここでは 0 固定）
        palette     [256][4]uint8
        bus         bus.Bus
        wg          sync.WaitGroup
    }
    ```

    **New() の変更 (R1)**:
    デフォルトサイズ定数を導入:
    ```go
    const (
        DefaultPageWidth  = 256
        DefaultPageHeight = 212
    )

    func New() *VRAMModule {
        return &VRAMModule{
            color: 8,
            pages: []pageBuffer{newPageBuffer(DefaultPageWidth, DefaultPageHeight)},
        }
    }
    ```

    **VRAMWidth() / VRAMHeight() の変更 (R1 → R9 の前段)**:
    表示ページのサイズを返す:
    ```go
    func (v *VRAMModule) VRAMWidth() int  { return v.pages[v.displayPage].width }
    func (v *VRAMModule) VRAMHeight() int { return v.pages[v.displayPage].height }
    ```

    **collectIndexData / collectRGBAData の変更 (R1)**:
    ページのサイズを参照するよう変更:
    ```go
    func (v *VRAMModule) collectIndexData(page, x, y, w, h int) []byte {
        pw := v.pages[page].width
        // vi := (y+row)*pw + (x + col) に変更
    }
    func (v *VRAMModule) collectRGBAData(page, x, y, w, h int) []byte {
        pw := v.pages[page].width
        // vi := (y+row)*pw + (x + col) に変更
    }
    ```

*   **Logic**:

    **draw_pixel ハンドラ (R1)**:
    `v.width`/`v.height` → `v.pages[dstPage].width`/`v.pages[dstPage].height`
    `bufIdx := y*v.width + x` → `bufIdx := y*v.pages[dstPage].width + x`

    **clear_vram ハンドラ (R1)**:
    対象ページの `width`/`height` でループ範囲を決定（既にスライス全体をイテレートしているのでバッファサイズが正しければ変更不要）。

    **mode ハンドラ (R1)**:
    各ページを自身のサイズで再確保:
    ```go
    case "mode":
        for i := range v.pages {
            v.pages[i] = newPageBuffer(v.pages[i].width, v.pages[i].height)
        }
    ```

    **handleBlitRect の変更 (R1, R10)**:
    *   src_page パラメータを削除。ヘッダーが11バイト→10バイトに短縮。
    *   データパース: `data[0]` = dstPage, `data[1:2]` = dstX, ..., `data[9]` = blendMode, `data[10:]` = pixelData
    *   ClipRect に `v.pages[dstPage].width`, `v.pages[dstPage].height` を渡す。
    *   ピクセル書き込み: `dstIdx := (cy+row)*v.pages[dstPage].width + (cx + col)`
    *   ブレンド時: `v.pages[srcPage].color[dstIdx*4]` → `v.pages[dstPage].color[dstIdx*4]`

    **handleBlitRectTransform の変更 (R1, R10)**:
    *   src_page パラメータを削除。ヘッダーが20バイト→19バイトに短縮。
    *   データパース: `data[0]` = dstPage, `data[1:2]` = dstX, ..., `data[18]` = blendMode, `data[19:]` = pixelData
    *   ClipRect に `v.pages[dstPage].width`, `v.pages[dstPage].height` を渡す。
    *   ブレンド時: `v.pages[srcPage].color` → `v.pages[dstPage].color`

    **handleReadRect の変更 (R1)**:
    *   ソースページのサイズを参照: `v.pages[srcPage].width`, `v.pages[srcPage].height`

    **handleCopyRect の変更 (R1, R4)**:
    *   ソース側読み取り: `v.pages[srcPage].width`, `v.pages[srcPage].height` でクリップ
    *   デスティネーション側書き込み: `v.pages[dstPage].width`, `v.pages[dstPage].height` で ClipRect
    *   `dstI := (cy+row)*v.pages[dstPage].width + (cx + col)`

    **handleSetPageCount の変更 (R3)**:
    新規ページをデフォルトサイズで作成:
    ```go
    for i := currentCount; i < newCount; i++ {
        v.pages = append(v.pages, newPageBuffer(DefaultPageWidth, DefaultPageHeight))
    }
    ```

    **handleCopyPage の変更 (R4)**:
    デスティネーションをソースと同サイズにリサイズしてからコピー:
    ```go
    func (v *VRAMModule) handleCopyPage(data []byte) {
        // ... validation ...
        if src != dst {
            // Resize destination to match source dimensions
            srcW, srcH := v.pages[src].width, v.pages[src].height
            if v.pages[dst].width != srcW || v.pages[dst].height != srcH {
                v.pages[dst] = newPageBuffer(srcW, srcH)
            }
            copy(v.pages[dst].index, v.pages[src].index)
            copy(v.pages[dst].color, v.pages[src].color)
        }
        // ... publish page_copied event ...
    }
    ```

    **handleSetPageSize 新規追加 (R2)**:
    ```go
    func (v *VRAMModule) handleSetPageSize(data []byte) {
        // Format: [page:u8][width_hi:u8][width_lo:u8][height_hi:u8][height_lo:u8]
        if len(data) < 5 { return }
        page := int(data[0])
        if !v.isValidPage(page) {
            v.publishPageError(0x03)
            return
        }
        w := int(data[1])<<8 | int(data[2])
        h := int(data[3])<<8 | int(data[4])
        v.pages[page] = newPageBuffer(w, h)
        // If display page was resized, reset viewport
        if page == v.displayPage {
            v.viewportX = 0
            v.viewportY = 0
        }
        // Publish page_size_changed event
        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "page_size_changed",
            Operation: bus.OpCommand,
            Data:      data[:5],
            Source:    v.Name(),
        })
    }
    ```

    **handleMessage の switch に追加**:
    ```go
    case "set_page_size":
        v.handleSetPageSize(msg.Data)
    ```

    **publishRectEvent の変更 (R1)**:
    ページの width を使ってデータ収集するため、変更は collectIndexData/collectRGBAData 側で吸収。

#### [MODIFY] [features/neurom/internal/modules/vram/blend.go](file://features/neurom/internal/modules/vram/blend.go)

*   **Description**: blend.go 自体は変更なし。BlendPixel, BlendMode の API は変更されないため、そのまま利用可能。

#### [MODIFY] [features/neurom/internal/modules/vram/transform.go](file://features/neurom/internal/modules/vram/transform.go)

*   **Description**: transform.go 自体は変更なし。TransformBlit の API は変更されないため、そのまま利用可能。

### Integration Tests

#### [MODIFY] [features/neurom/integration/vram_enhancement_test.go](file://features/neurom/integration/vram_enhancement_test.go)

*   **Description**: blit_rect / blit_rect_transform の統合テストを新ワイヤフォーマット（src_page なし）に更新する。
*   **Technical Design**:
    *   `blitData` の構築で src_page バイトを除去（11バイトヘッダー → 10バイトヘッダー）。
    *   `blit_rect_transform` のデータ構築で src_page バイトを除去（20バイトヘッダー → 19バイトヘッダー）。

#### [MODIFY] [features/neurom/integration/vram_page_test.go](file://features/neurom/integration/vram_page_test.go)

*   **Description**: ページ管理の統合テストを更新し、ページサイズ関連テストを追加する。
*   **Technical Design**:

    **既存テストの修正**:
    *   blit_rect を使う箇所の wire format を新仕様に更新。

    **新規テスト追加**:
    ```go
    func TestPageSizeManagementIntegration(t *testing.T) {
        // (1) set_page_count(3) → page_count_changed
        // (2) set_page_size(page=0, 512, 424) → page_size_changed
        // (3) draw_pixel at (300, 300) on page 0 → 成功
        // (4) set_page_size(page=2, 128, 128) → page_size_changed
        // (5) copy_page(0→1) → page 1 が 512×424 にリサイズされる
        // (6) copy_rect(page0 → page2, 200×200) → page2 (128×128) にクリップ
    }
    ```

    **ダブルバッファ＋大マップ デモ テスト (シナリオ12)**:
    ```go
    func TestDoubleBufferLargeMapDemo(t *testing.T) {
        // セットアップ: 3ページ、page2を1024×1024に
        // フレーム1: read_rect → blit_rect (スクロール)
        // フレーム2: 別位置から read_rect → blit_rect
        // フレーム3: read_rect → blit_rect_transform (回転+拡大)
        // 各フレームで set_display_page で切替
    }
    ```

#### [MODIFY] [features/neurom/integration/vram_monitor_test.go](file://features/neurom/integration/vram_monitor_test.go)

*   **Description**: blit_rect を使う統合テストの wire format を新仕様に更新する。

#### [MODIFY] [features/neurom/integration/monitor_sync_test.go](file://features/neurom/integration/monitor_sync_test.go)

*   **Description**: blit_rect を使う統合テストの wire format を新仕様に更新（ある場合）。

## Step-by-Step Implementation Guide

### Phase 1: テスト作成（TDD — Red Phase）

1.  [x] **pageBuffer サイズ保持テスト作成**:
    *   Edit `features/neurom/internal/modules/vram/vram_test.go`
    *   `TestPageBufferHasSize` を追加。`newPageBuffer(w, h)` が `width`/`height` を保持し、`New()` のデフォルトページが 256×212 であることを検証。

2.  [x] **既存テストのヘルパー関数修正（R10）**:
    *   Edit `features/neurom/internal/modules/vram/vram_test.go`
    *   `buildBlitRectMsgPage` を修正: src_page 引数を削除、data 構築を 10 バイトヘッダーに変更。
    *   `buildBlitRectMsg` を修正: src_page 引数を削除。
    *   `buildBlitRectTransformMsgPage` を修正: src_page 引数を削除、data 構築を 19 バイトヘッダーに変更。
    *   `buildBlitRectTransformMsg` を修正: src_page 引数を削除。
    *   全呼び出し元のコードを修正。

3.  [x] **新規テスト追加**:
    *   `TestSetPageSize`, `TestDrawPixelOnLargePage`, `TestSetPageCountNewPagesDefaultSize`, `TestCopyPageResizesDestination`, `TestCopyRectCrossPageDifferentSizes`, `TestBlitRectNewFormat`, `TestBlitRectBlendFromDstPage`, `TestBlitRectTransformNewFormat` を追加。
    *   追加テスト: `TestSetPageSizeResetsViewportOnDisplayPage`, `TestSetPageSizeDoesNotResetViewportOnNonDisplayPage`

4.  [x] **`build.sh` を実行してテストが失敗することを確認**。

### Phase 2: 実装（TDD — Green Phase）

5.  [x] **pageBuffer 構造体に width/height を追加**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `pageBuffer` に `width`, `height` フィールドを追加。
    *   `newPageBuffer` を `width`/`height` を設定するよう変更。

6.  [x] **VRAMModule から width/height を削除**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `VRAMModule` の `width`, `height` フィールドを削除。
    *   `viewportX`, `viewportY` フィールドを追加（初期値 0）。
    *   定数 `DefaultPageWidth`, `DefaultPageHeight` を追加。
    *   `New()` を修正。

7.  [x] **VRAMWidth/VRAMHeight を表示ページ参照に変更**:
    *   `VRAMWidth()` → `v.pages[v.displayPage].width`
    *   `VRAMHeight()` → `v.pages[v.displayPage].height`
    *   `ViewportOffset()` メソッドも追加。

8.  [x] **全コマンドハンドラのリファクタリング (R1)**:
    *   `draw_pixel`: `v.width`/`v.height` → `v.pages[dstPage].width`/`.height`
    *   `clear_vram`: 変更不要（スライス全体をイテレートしているため）
    *   `mode`: 各ページを自身のサイズで再確保
    *   `handleBlitRect`: src_page 削除、オフセット調整、ページサイズ参照、ブレンド背景を dstPage から
    *   `handleBlitRectTransform`: 同上
    *   `handleReadRect`: ソースページのサイズ参照
    *   `handleCopyRect`: ソース/デスティネーション各ページのサイズ参照
    *   `collectIndexData`, `collectRGBAData`: ページの width 参照
    *   `publishRectEvent`: 変更不要（collectXxx 側で吸収）

9.  [x] **handleSetPageCount の変更 (R3)**:
    *   新規ページを `newPageBuffer(DefaultPageWidth, DefaultPageHeight)` で作成。

10. [x] **handleCopyPage の変更 (R4)**:
    *   デスティネーションをソースと同サイズにリサイズしてからコピー。

11. [x] **handleSetPageSize の新規追加 (R2)**:
    *   ワイヤフォーマット: `[page:u8][width_hi][width_lo][height_hi][height_lo]`
    *   バッファ再確保、表示ページならビューポートリセット、イベント発行。
    *   `handleMessage` の switch に `"set_page_size"` ケースを追加。

12. [x] **`build.sh` を実行して単体テストがパスすることを確認**。

### Phase 3: 統合テスト更新

13. [x] **統合テストの blit_rect wire format を更新**:
    *   Edit `features/neurom/integration/vram_enhancement_test.go`
    *   Edit `features/neurom/integration/vram_page_test.go`
    *   Edit `features/neurom/integration/vram_monitor_test.go` (変更不要だった)
    *   Edit `features/neurom/integration/monitor_sync_test.go`
    *   Edit `features/neurom/integration/bus_panic_guard_test.go`
    *   Edit `features/neurom/internal/modules/cpu/cpu.go` (デモプログラムも更新)
    *   blit_rect / blit_rect_transform の data 構築から src_page バイトを削除。

14. [x] **新規統合テスト追加**:
    *   Edit `features/neurom/integration/vram_page_test.go`
    *   `TestPageSizeManagementIntegration` を追加。
    *   `TestDoubleBufferLargeMapDemo` を追加。

15. [x] **統合テスト実行**:
    *   `build.sh` で統合テスト含む全テストを実行して確認。

### Phase 4: 既存テストのリグレッション確認

16. [x] **全テスト実行**:
    ```bash
    ./scripts/process/build.sh
    ```
    *   全テスト PASS。Exit code 0。

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests (新規テスト)**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestPageSizeManagementIntegration"
    ./scripts/process/integration_test.sh --specify "TestDoubleBufferLargeMapDemo"
    ```

3.  **Integration Tests (全件リグレッション)**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **Log Verification**:
        *   `page_size_changed` イベントが正しいフォーマットで発行されること
        *   `page_error` が不正ページ番号で発行されること
        *   既存テスト（`TestBlitAndReadRect`, `TestPageManagementIntegration` 等）が引き続きパスすること

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md](file://prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md)
*   **更新内容**: 実装完了後、検証シナリオのステータスを更新。

## 継続計画について

本計画は Part 1 であり、以下の要件は **Part 2 (013-VRAMViewport-Part2.md)** で実装する:

- R5: モニタ解像度の独立設定
- R6: 表示オフセット（ビューポート）の設定
- R7: ビューポートのクリッピング動作
- R8: VRAMAccessor インターフェースの拡張
- R9: VRAMWidth / VRAMHeight の互換対応（Part 1 で前段実装済み、Part 2 で MonitorModule 連携確認）
- シナリオ4〜6, 9, 11: ビューポート関連テスト
- シナリオ12 のフレーム3（blit_rect_transform 部分は Part 1 で基盤実装済み）

Part 1 完了後に Part 2 の実装に進む。
