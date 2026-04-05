# 012-VRAMPage-Part2

> **Source Specification**: [prompts/phases/000-foundation/ideas/main/012-VRAMPage.md](file://prompts/phases/000-foundation/ideas/main/012-VRAMPage.md)

## Goal Description

Part1 で導入したページデータ構造の上に、全描画・転送コマンドのページ引数化（R5）、Monitorモジュールとの連携（R6）を実装する。CPUデモコード・既存テスト・統合テストの更新も含む。

## User Review Required

None.

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R1: ページデータ構造の導入 | Part1 で実装済み |
| R2: ページ数設定 (`set_page_count`) | Part1 で実装済み |
| R3: 表示ページ設定 (`set_display_page`) | Part1 で実装済み |
| R4-1: ページスワップ (`swap_pages`) | Part1 で実装済み |
| R4-2: ページコピー (`copy_page`) | Part1 で実装済み |
| R5: 全コマンドのページ引数化 | Proposed Changes > vram/vram.go、vram/vram_test.go |
| R6: Monitorモジュールとの連携 | Proposed Changes > monitor/monitor.go、monitor/monitor_test.go |

## Proposed Changes

### vram モジュール

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: 既存テストのコマンドフォーマットをページ引数付きに更新し、R5 のページ分離テストを追加する。
*   **Technical Design**:

    **既存テストの更新パターン:**

    全てのテストで `handleMessage` に渡す `Data` バイトスライスの先頭にページ番号を追加する。

    ```go
    // 旧: draw_pixel Data: [x:u16][y:u16][p:u8]
    //   例: []byte{0x00, 0x03, 0x00, 0x04, 0x0A}
    // 新: draw_pixel Data: [dst_page:u8][x:u16][y:u16][p:u8]
    //   例: []byte{0x00, 0x00, 0x03, 0x00, 0x04, 0x0A}  // page=0

    // 旧: clear_vram Data: [palette_idx:u8]
    //   例: []byte{0x05}
    // 新: clear_vram Data: [dst_page:u8][palette_idx:u8]
    //   例: []byte{0x00, 0x05}  // page=0

    // 旧: blit_rect Data: [dst_x:u16][dst_y:u16][w:u16][h:u16][blend_mode:u8][pixels...]
    // 新: blit_rect Data: [src_page:u8][dst_page:u8][dst_x:u16]...
    //   先頭に src_page=0, dst_page=0 の2バイトを追加

    // 旧: blit_rect_transform Data: [dst_x:u16]...
    // 新: blit_rect_transform Data: [src_page:u8][dst_page:u8][dst_x:u16]...
    //   先頭に src_page=0, dst_page=0 の2バイトを追加

    // 旧: copy_rect Data: [src_x:u16][src_y:u16][dst_x:u16][dst_y:u16][w:u16][h:u16]
    // 新: copy_rect Data: [src_page:u8][dst_page:u8][src_x:u16]...
    //   先頭に src_page=0, dst_page=0 の2バイトを追加

    // 旧: read_rect Data: [x:u16][y:u16][w:u16][h:u16]
    // 新: read_rect Data: [src_page:u8][x:u16]...
    //   先頭に src_page=0 の1バイトを追加
    ```

    **新規テスト:**

    ```go
    // TestDrawPixelPageIsolation: R5 ページ分離
    // - set_page_count(2)
    // - draw_pixel(page=0, x=5, y=5, p=10)
    // - draw_pixel(page=1, x=5, y=5, p=20)
    // - pages[0].index[5*256+5] == 10
    // - pages[1].index[5*256+5] == 20

    // TestClearVramPageIsolation: R5 ページ分離
    // - set_page_count(2)
    // - draw_pixel(page=0, x=0, y=0, p=5)  // page 0 に何か描く
    // - clear_vram(page=1, palette_idx=7)
    // - pages[0].index[0] == 5  (変更なし)
    // - pages[1] は全て 7

    // TestBlitRectPageIsolation: R5 ページ分離
    // - set_page_count(2)
    // - blit_rect(src=0, dst=0, ...) でページ0に描画
    // - pages[1] はゼロクリアのまま
    // - blit_rect(src=1, dst=1, ...) でページ1に描画
    // - pages[0] は最初の描画のまま

    // TestBlitRectCrossPageBlend: R5 src_page ≠ dst_page
    // - set_page_count(2)
    // - ページ0 の座標(0,0) にパレット1のピクセルを描画（背景）
    // - blit_rect(src_page=0, dst_page=1, blend=Alpha) で合成
    // - pages[0] は変更なし
    // - pages[1] にブレンド結果が書き込まれている

    // TestBlitRectTransformPageIsolation: R5
    // - blit_rect_transform で src/dst ページを指定し、分離を確認

    // TestCopyRectCrossPage: R5 ページ間コピー
    // - set_page_count(2)
    // - ページ0 に矩形データを描画
    // - copy_rect(src_page=0, dst_page=1, ...) で領域コピー
    // - ページ1 にコピーされていること
    // - ページ0 は変更なし

    // TestReadRectPage: R5 ページ指定読み取り
    // - set_page_count(2)
    // - ページ1 にデータを描画
    // - read_rect(src_page=1, ...) → 正しいデータ
    // - read_rect(src_page=0, ...) → ゼロデータ

    // TestInvalidPageError: R5 無効ページエラー
    // - draw_pixel(page=5) → page_error
    // - blit_rect(src=0, dst=5) → page_error
    // - copy_rect(src=5, dst=0) → page_error
    // - read_rect(page=5) → page_error
    ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: 全ての描画・転送コマンドのバイナリフォーマットにページ引数を追加する。
*   **Technical Design**:

    **handleMessage の draw_pixel:**

    ```go
    case "draw_pixel":
        // 新フォーマット: [dst_page:u8][x:u16][y:u16][p:u8] → 6バイト以上
        if len(msg.Data) < 6 { return }
        dstPage := int(msg.Data[0])
        if !v.isValidPage(dstPage) {
            v.publishPageError(0x03)
            return
        }
        x := int(msg.Data[1])<<8 | int(msg.Data[2])
        y := int(msg.Data[3])<<8 | int(msg.Data[4])
        p := msg.Data[5]
        // v.pages[dstPage].index と v.pages[dstPage].color に書き込む
        // （既存ロジックの v.buffer → v.pages[dstPage].index 等に置換）
    ```

    **handleMessage の clear_vram:**

    ```go
    case "clear_vram":
        // 新フォーマット: [dst_page:u8][palette_idx:u8] → 1バイト以上
        if len(msg.Data) < 1 { return }
        dstPage := int(msg.Data[0])
        if !v.isValidPage(dstPage) {
            v.publishPageError(0x03)
            return
        }
        paletteIdx := uint8(0)
        if len(msg.Data) >= 2 {
            paletteIdx = msg.Data[1]
        }
        buf := v.pages[dstPage]
        // buf.index を paletteIdx で埋める
        // buf.color を palette[paletteIdx] の RGBA で埋める
        // vram_cleared イベント発行
    ```

    **handleBlitRect の変更:**

    ```go
    func (v *VRAMModule) handleBlitRect(data []byte) {
        // 新フォーマット: [src_page:u8][dst_page:u8][dst_x:u16]...[blend_mode:u8][pixels...]
        // → 最小 11 バイト (2 + 旧9)
        if len(data) < 11 { return }
        srcPage := int(data[0])
        dstPage := int(data[1])
        if !v.isValidPage(srcPage) || !v.isValidPage(dstPage) {
            v.publishPageError(0x03)
            return
        }
        dstX := int(data[2])<<8 | int(data[3])
        dstY := int(data[4])<<8 | int(data[5])
        w := int(data[6])<<8 | int(data[7])
        h := int(data[8])<<8 | int(data[9])
        blendMode := BlendMode(data[10])
        pixelData := data[11:]
        // ブレンド計算: src_page のバッファから背景を読み、dst_page に結果を書く
        // BlendReplace 時は src_page を参照しない
        // 既存ロジックの v.buffer → v.pages[dstPage].index
        // v.colorBuffer → v.pages[dstPage].color
        // ブレンド時の dstRGBA は v.pages[srcPage].color から読む
    }
    ```

    **handleBlitRectTransform の変更:**

    ```go
    func (v *VRAMModule) handleBlitRectTransform(data []byte) {
        // 新フォーマット: [src_page:u8][dst_page:u8][dst_x:u16]...[blend_mode:u8][pixels...]
        // → 最小 20 バイト (2 + 旧18)
        if len(data) < 20 { return }
        srcPage := int(data[0])
        dstPage := int(data[1])
        if !v.isValidPage(srcPage) || !v.isValidPage(dstPage) {
            v.publishPageError(0x03)
            return
        }
        // data[2:] 以降は旧フォーマットと同じオフセット
        dstX := int(data[2])<<8 | int(data[3])
        // ... 以下同様にオフセット+2
        // ブレンド時の背景は v.pages[srcPage] から読む
        // 結果は v.pages[dstPage] に書く
    }
    ```

    **handleCopyRect の変更:**

    ```go
    func (v *VRAMModule) handleCopyRect(data []byte) {
        // 新フォーマット: [src_page:u8][dst_page:u8][src_x:u16]...[w:u16][h:u16]
        // → 最小 14 バイト (2 + 旧12)
        if len(data) < 14 { return }
        srcPage := int(data[0])
        dstPage := int(data[1])
        if !v.isValidPage(srcPage) || !v.isValidPage(dstPage) {
            v.publishPageError(0x03)
            return
        }
        srcX := int(data[2])<<8 | int(data[3])
        srcY := int(data[4])<<8 | int(data[5])
        dstX := int(data[6])<<8 | int(data[7])
        dstY := int(data[8])<<8 | int(data[9])
        w := int(data[10])<<8 | int(data[11])
        h := int(data[12])<<8 | int(data[13])

        // ソースバッファ: v.pages[srcPage]
        // デスティネーションバッファ: v.pages[dstPage]
        // 同一ページ内コピーの場合はオーバーラップ対応（tmpバッファ経由）を維持
        // 異なるページ間コピーの場合は直接コピー可能（オーバーラップなし）
    }
    ```

    **handleReadRect の変更:**

    ```go
    func (v *VRAMModule) handleReadRect(data []byte) {
        // 新フォーマット: [src_page:u8][x:u16][y:u16][w:u16][h:u16]
        // → 最小 9 バイト (1 + 旧8)
        if len(data) < 9 { return }
        srcPage := int(data[0])
        if !v.isValidPage(srcPage) {
            v.publishPageError(0x03)
            return
        }
        x := int(data[1])<<8 | int(data[2])
        y := int(data[3])<<8 | int(data[4])
        w := int(data[5])<<8 | int(data[6])
        h := int(data[7])<<8 | int(data[8])
        // v.pages[srcPage].index から読み取り
    }
    ```

    **publishRectEvent / collectIndexData / collectRGBAData の変更:**

    これらのヘルパー関数にページ番号を引数として追加する。

    ```go
    func (v *VRAMModule) publishRectEvent(page, x, y, w, h int) {
        // page 番号をイベントデータの先頭に追加
        // 既存のヘッダ [x:u16][y:u16][w:u16][h:u16][mode:u8] の前に [page:u8] を追加
    }

    func (v *VRAMModule) collectIndexData(page, x, y, w, h int) []byte {
        // v.pages[page].index からデータ収集
    }

    func (v *VRAMModule) collectRGBAData(page, x, y, w, h int) []byte {
        // v.pages[page].color からデータ収集
    }

    func (v *VRAMModule) publishCopyEvent(srcPage, srcX, srcY, dstPage, dstX, dstY, w, h int) {
        // ページ番号を含むコピーイベント発行
    }
    ```

### monitor モジュール

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: R6 に基づき、displayPage のバッファを読み取るよう更新する。
*   **Technical Design**:

    **Directモード（変更なし）:**

    `VRAMAccessor.VRAMBuffer()` / `VRAMColorBuffer()` が Part1 で `pages[displayPage]` を返すよう変更済みのため、`refreshFromVRAM()` / `directRefreshLoop()` は変更不要。

    **Busモード:**

    `handleVRAMUpdate` に以下の変更を加える:

    ```go
    // MonitorModule に displayPage フィールドを追加
    type MonitorModule struct {
        // ... 既存フィールド ...
        displayPage int // tracks current display page for bus mode filtering
    }
    ```

    **handleVRAMUpdate の更新:**

    ```go
    func (m *MonitorModule) handleVRAMUpdate(msg *bus.BusMessage) {
        if m.vramAccessor != nil { return }

        switch msg.Target {
        case "display_page_changed":
            // 表示ページ変更: キャッシュ無効化
            if len(msg.Data) >= 1 {
                m.mu.Lock()
                m.displayPage = int(msg.Data[0])
                // 内部バッファをゼロクリア（次のフルリフレッシュに備える）
                for i := range m.vram {
                    m.vram[i] = 0
                    m.hasDirectPixel[i] = false
                }
                m.dirty = true
                m.mu.Unlock()
                m.requestPaint()
            }

        case "page_count_changed":
            // ページ数変更自体はMonitorに影響なし（displayPage のリセットは
            // display_page_changed で通知される場合のみ対応）

        case "rect_updated":
            // 新フォーマット: [page:u8][x:u16][y:u16][w:u16][h:u16][mode:u8][data...]
            if len(msg.Data) < 10 { break }
            page := int(msg.Data[0])
            if page != m.displayPage { break } // 非表示ページは無視
            // 以降のパースは msg.Data[1:] にオフセット
            // 既存ロジックをそのまま適用（インデックスを+1ずらす）

        case "vram_updated":
            // draw_pixel イベントにページ番号が含まれる場合
            // 新フォーマット: [page:u8][x:u16][y:u16][p:u8]
            if len(msg.Data) < 6 { break }
            page := int(msg.Data[0])
            if page != m.displayPage { break }
            x := int(msg.Data[1])<<8 | int(msg.Data[2])
            y := int(msg.Data[3])<<8 | int(msg.Data[4])
            p := msg.Data[5]
            // 以降は既存ロジック

        case "vram_cleared":
            // 新フォーマット: [page:u8][palette_idx:u8]
            if len(msg.Data) < 2 { break }
            page := int(msg.Data[0])
            if page != m.displayPage { break }
            palIdx := msg.Data[1]
            // 以降は既存ロジック

        case "rect_copied":
            // 新フォーマット: ページ番号を含む
            // displayPage に関わるコピーの場合のみ処理

        // palette_updated, palette_block_updated, palette_data は
        // ページに依存しないため変更なし

        // mode_changed も変更なし（全ページ初期化）
        }
    }
    ```

### cpu モジュール

#### [MODIFY] [features/neurom/internal/modules/cpu/cpu.go](file://features/neurom/internal/modules/cpu/cpu.go)

*   **Description**: 全てのデモシーンのコマンド発行ヘルパーにページ引数を追加する。
*   **Technical Design**:

    **ヘルパー関数のシグネチャ変更:**

    ```go
    func publishClearVRAM(b bus.Bus, page byte, paletteIdx byte) {
        // Data: [dst_page:u8][palette_idx:u8]
        _ = b.Publish("vram", &bus.BusMessage{
            Target: "clear_vram", Operation: bus.OpCommand,
            Data: []byte{page, paletteIdx}, Source: "CPU",
        })
    }

    func publishBlitRect(b bus.Bus, srcPage, dstPage byte, x, y, w, h int, blendMode byte, pixels []byte) {
        // Data: [src_page:u8][dst_page:u8][dst_x:u16]...[blend_mode:u8][pixels...]
        data := make([]byte, 11+len(pixels))
        data[0] = srcPage
        data[1] = dstPage
        data[2] = byte(x >> 8)
        data[3] = byte(x)
        // ... 以降は旧フォーマットのオフセット+2
    }

    func publishBlitRectTransform(b bus.Bus, srcPage, dstPage byte, dstX, dstY, w, h, pivotX, pivotY int, rotation byte, scaleX, scaleY uint16, blendMode byte, pixels []byte) {
        // Data: [src_page:u8][dst_page:u8][dst_x:u16]...
        data := make([]byte, 20+len(pixels))
        data[0] = srcPage
        data[1] = dstPage
        // ... 以降は旧フォーマットのオフセット+2
    }

    func publishCopyRect(b bus.Bus, srcPage, dstPage byte, srcX, srcY, dstX, dstY, w, h int) {
        // Data: [src_page:u8][dst_page:u8][src_x:u16]...
        data := make([]byte, 14)
        data[0] = srcPage
        data[1] = dstPage
        // ... 以降は旧フォーマットのオフセット+2
    }
    ```

    **各シーンの呼び出し箇所の更新:**

    全てのシーンで `publishClearVRAM(b, 0)` → `publishClearVRAM(b, 0, 0)` のようにページ引数を追加する。全デモシーンはページ0を使用するため、`srcPage=0, dstPage=0` を指定する。

### integration テスト

#### [MODIFY] [features/neurom/integration/vram_enhancement_test.go](file://features/neurom/integration/vram_enhancement_test.go)

*   **Description**: 既存の統合テストのコマンドフォーマットをページ引数付きに更新する。
*   **Technical Design**:
    *   全ての `b.Publish("vram", ...)` で送信する `Data` の先頭にページ番号を追加する
    *   パターンは vram_test.go の更新と同一（draw_pixel → +1バイト先頭、blit_rect → +2バイト先頭、等）

#### [MODIFY] [features/neurom/integration/vram_monitor_test.go](file://features/neurom/integration/vram_monitor_test.go)

*   **Description**: 既存のVRAM-Monitor統合テストのコマンドフォーマットを更新する。

#### [MODIFY] [features/neurom/integration/monitor_sync_test.go](file://features/neurom/integration/monitor_sync_test.go)

*   **Description**: 既存のMonitor同期テストのコマンドフォーマットを更新する。

#### [MODIFY] [features/neurom/integration/palette_test.go](file://features/neurom/integration/palette_test.go)

*   **Description**: パレットテストのコマンドフォーマットを更新する。

#### [MODIFY] [features/neurom/integration/bus_panic_guard_test.go](file://features/neurom/integration/bus_panic_guard_test.go)

*   **Description**: バスパニックガードテストのコマンドフォーマットを更新する。

#### [MODIFY] [features/neurom/integration/shutdown_test.go](file://features/neurom/integration/shutdown_test.go)

*   **Description**: シャットダウンテストのコマンドフォーマットを更新する。

#### [NEW] [features/neurom/integration/vram_page_test.go](file://features/neurom/integration/vram_page_test.go)

*   **Description**: ページ機能の統合テストを新規作成する。
*   **Technical Design**:

    ```go
    // TestPageManagementIntegration: R2-R4 統合テスト
    // - ChannelBus + VRAMModule + MonitorModule(Headless) を起動
    // - vram_update を Subscribe してイベントを検証
    //
    // (1) set_page_count(4) → page_count_changed イベント (count=4) を確認
    // (2) set_display_page(2) → display_page_changed (page=2) を確認
    // (3) swap_pages(0, 1) → pages_swapped (0, 1) を確認
    // (4) copy_page(0, 1) → page_copied (0, 1) を確認
    // (5) set_page_count(1) → page_count_changed (count=1) を確認

    // TestPageDrawIsolationIntegration: R5 統合テスト
    // - set_page_count(2)
    // - blit_rect(src=0, dst=0, ...) でページ0に描画
    // - blit_rect(src=1, dst=1, ...) でページ1に描画
    // - read_rect(src_page=0) → ページ0のデータを確認
    // - read_rect(src_page=1) → ページ1のデータを確認
    // - 両ページのデータが独立していることをバス経由で確認

    // TestPageCrossPageCopyIntegration: R5 ページ間コピー統合テスト
    // - set_page_count(2)
    // - blit_rect(src=0, dst=0) でページ0に描画
    // - copy_rect(src_page=0, dst_page=1) でページ間コピー
    // - read_rect(src_page=1) でページ1のデータがページ0と同じか確認

    // TestPageDisplayIntegration: R6 表示ページ統合テスト
    // - MonitorModule を SetVRAMAccessor で Direct モードに設定
    // - set_page_count(2)
    // - ページ0にパターンA、ページ1にパターンBを描画
    // - set_display_page(0): GetPixel で パターンA が見えることを確認
    // - set_display_page(1): GetPixel で パターンB が見えることを確認

    // TestPageErrorIntegration: エラーケース統合テスト
    // - draw_pixel(page=5) → page_error イベントを確認
    // - set_display_page(page=5) → page_error イベントを確認
    // - swap_pages(0, 5) → page_error イベントを確認
    ```

## Step-by-Step Implementation Guide

1.  [x] **vram_test.go: 既存テストのページ引数更新 + R5 新規テスト作成（TDD）**:
    *   Edit `features/neurom/internal/modules/vram/vram_test.go`
    *   全ての `handleMessage` 呼び出しの `Data` にページ引数を追加
    *   R5 のページ分離テスト・エラーテストを追加

2.  [x] **vram.go: 全コマンドハンドラのページ引数対応**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `draw_pixel`, `clear_vram` のハンドラを handleMessage 内でインライン更新
    *   `handleBlitRect`, `handleBlitRectTransform`, `handleCopyRect`, `handleReadRect` のパースオフセットを変更
    *   `publishRectEvent`, `collectIndexData`, `collectRGBAData`, `publishCopyEvent` にページ引数を追加

3.  [x] **ビルドと単体テスト**:
    *   `./scripts/process/build.sh` で全テストがパスすることを確認

4.  [x] **monitor.go: Busモードのページフィルタリング**:
    *   Edit `features/neurom/internal/modules/monitor/monitor.go`
    *   `displayPage` フィールドを追加
    *   `handleVRAMUpdate` にページフィルタリングロジックを追加
    *   `display_page_changed` ハンドラを追加

5.  [x] **cpu.go: ヘルパー関数のページ引数追加**:
    *   Edit `features/neurom/internal/modules/cpu/cpu.go`
    *   `publishClearVRAM`, `publishBlitRect`, `publishBlitRectTransform`, `publishCopyRect` にページ引数を追加
    *   全シーンの呼び出し箇所を更新（全て page=0）

6.  [x] **既存統合テストの更新**:
    *   Edit `features/neurom/integration/vram_enhancement_test.go` 等
    *   全コマンドの Data にページ引数を追加

7.  [x] **新規統合テストの作成**:
    *   Create `features/neurom/integration/vram_page_test.go`
    *   ページ管理、ページ分離、表示ページ、エラーケースの統合テスト

8.  [x] **ビルドと全テスト**:
    *   `./scripts/process/build.sh` で全テストがパスすることを確認

9.  [x] **統合テスト**:
    *   統合テストは `build.sh` の Go テスト実行に含まれ、全てパスを確認

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests**:
    ```bash
    ./scripts/process/integration_test.sh --specify "vram_page"
    ```
    *   **Log Verification**: `page_count_changed`, `display_page_changed`, `pages_swapped`, `page_copied`, `page_error` イベントが正しく発行されていること

3.  **Full Integration Tests (Regression)**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **Log Verification**: 既存の統合テストが全てパスすること（ページ引数追加による破壊的変更がないこと）

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/012-VRAMPage.md](file://prompts/phases/000-foundation/ideas/main/012-VRAMPage.md)
*   **更新内容**: 実装完了ステータスの記録（必要に応じて）
