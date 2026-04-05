# 013-VRAMViewport-Part2

> **Source Specification**: [prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md](file://prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md)

## Goal Description

Part 1 で完了したページ単位サイズ管理の基盤の上に、ビューポート機能（表示オフセット）とMonitorModuleの解像度独立設定を実装する。R5〜R9 を対象とし、ビューポート関連のテストシナリオとデモの統合テストを追加する。

## User Review Required

- R5 MonitorConfig の表示解像度フィールド名: `DisplayWidth` / `DisplayHeight` を提案。既存の `Headless`, `OnClose`, `RefreshRate` と並ぶフィールドとする。
- R8 VRAMAccessor に追加するメソッド: `ViewportOffset() (x, y int)` を提案。既存の `VRAMWidth()` / `VRAMHeight()` は Part 1 で表示ページサイズを返すよう変更済みのため、新たに追加するのはビューポートオフセットのみ。

## Requirement Traceability

| Requirement (from Spec) | Implementation Point (Section/File) |
| :--- | :--- |
| R5: モニタ解像度の独立設定 | Proposed Changes > monitor.go (MonitorConfig拡張, New修正) |
| R6: 表示オフセット（ビューポート）の設定 | Proposed Changes > vram.go (set_viewport ハンドラ追加) |
| R7: ビューポートのクリッピング動作 | Proposed Changes > monitor.go (refreshFromVRAM / handleVRAMUpdate 修正) |
| R8: VRAMAccessor インターフェースの拡張 | Proposed Changes > monitor.go (VRAMAccessor に ViewportOffset 追加) |
| R9: VRAMWidth / VRAMHeight の互換対応 | Part 1 で前段実装済み。本 Part では MonitorModule 側の動作確認。 |
| R1〜R4, R10 | **Part 1 で実装済み** |

## Proposed Changes

### VRAM Module

#### [MODIFY] [features/neurom/internal/modules/vram/vram_test.go](file://features/neurom/internal/modules/vram/vram_test.go)

*   **Description**: ビューポート関連の単体テストを追加する。
*   **Technical Design**:

    **1. set_viewport テスト (R6)**:
    ```go
    func TestSetViewport(t *testing.T) {
        tests := []struct {
            name    string
            offsetX int16
            offsetY int16
        }{
            {"positive_offset", 100, 50},
            {"zero_offset", 0, 0},
            {"negative_offset", -10, -5},
        }
        // set_viewport コマンド送信 → viewport_changed イベント検証
        // ViewportOffset() でオフセットが正しく返ることを検証
    }
    ```

    **2. ViewportOffset アクセサテスト (R8)**:
    ```go
    func TestViewportOffset(t *testing.T) {
        // New() 直後のデフォルト値が (0, 0)
        // set_viewport で変更後の値が正しく返ること
    }
    ```

    **3. 表示ページサイズ変更時のビューポートリセットテスト (R2 + R6)**:
    ```go
    func TestSetPageSizeResetsViewportOnDisplayPage(t *testing.T) {
        // set_viewport で (100, 50) に設定
        // set_page_size で表示ページをリサイズ
        // ViewportOffset() が (0, 0) にリセットされることを検証
    }

    func TestSetPageSizeDoesNotResetViewportOnNonDisplayPage(t *testing.T) {
        // page 0 を表示ページに設定
        // set_viewport で (100, 50) に設定
        // set_page_size で page 1 をリサイズ
        // ViewportOffset() が (100, 50) のまま変わらないことを検証
    }
    ```

#### [MODIFY] [features/neurom/internal/modules/vram/vram.go](file://features/neurom/internal/modules/vram/vram.go)

*   **Description**: set_viewport コマンドハンドラと ViewportOffset アクセサを追加する。
*   **Technical Design**:

    **ViewportOffset アクセサ (R8)**:
    ```go
    func (v *VRAMModule) ViewportOffset() (x, y int) {
        return v.viewportX, v.viewportY
    }
    ```

    **handleSetViewport 新規追加 (R6)**:
    ```go
    func (v *VRAMModule) handleSetViewport(data []byte) {
        // Format: [offsetX_hi:u8][offsetX_lo:u8][offsetY_hi:u8][offsetY_lo:u8] (int16 BE)
        if len(data) < 4 { return }
        v.viewportX = int(int16(data[0])<<8 | int16(data[1]))
        v.viewportY = int(int16(data[2])<<8 | int16(data[3]))
        v.bus.Publish("vram_update", &bus.BusMessage{
            Target:    "viewport_changed",
            Operation: bus.OpCommand,
            Data:      data[:4],
            Source:    v.Name(),
        })
    }
    ```
    *   `int16` キャストにより負のオフセットを正しくデコードする。

    **handleMessage の switch に追加**:
    ```go
    case "set_viewport":
        v.handleSetViewport(msg.Data)
    ```

### Monitor Module

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor_test.go](file://features/neurom/internal/modules/monitor/monitor_test.go)

*   **Description**: ビューポートクリッピングの表示テストを追加する。
*   **Technical Design**:

    **1. モニタ解像度の独立設定テスト (R5)**:
    ```go
    func TestMonitorCustomDisplaySize(t *testing.T) {
        m := New(MonitorConfig{
            Headless:      true,
            DisplayWidth:  128,
            DisplayHeight: 128,
        })
        // m.width, m.height が 128, 128 であることを検証
        // バッファサイズが 128*128 であることを検証
    }

    func TestMonitorDefaultDisplaySize(t *testing.T) {
        m := New(MonitorConfig{Headless: true})
        // m.width, m.height がデフォルト 256, 212 であることを検証
    }
    ```

    **2. ビューポートクリッピングテスト (R7, R8)**:

    ビューポートのクリッピングはMonitorModuleの `refreshFromVRAM()` で実装される。テストのために、VRAMAccessor をモック化する:

    ```go
    type mockVRAMAccessor struct {
        width     int
        height    int
        index     []uint8
        color     []uint8
        palette   [256][4]uint8
        vpX, vpY  int
    }
    // VRAMAccessor インターフェースのメソッドを実装
    // ViewportOffset() (x, y int) を追加

    func TestRefreshFromVRAMWithViewport(t *testing.T) {
        // 512×424 の VRAM モック
        // モニタ 256×212
        // ビューポート offset (100, 50)
        // VRAM の (100, 50) のピクセルが モニタの (0, 0) に表示されること
    }

    func TestRefreshFromVRAMClipping(t *testing.T) {
        // 256×212 の VRAM モック
        // ビューポート offset (200, 0)
        // (200,0)-(255,211) の範囲がモニタの (0,0)-(55,211) に表示
        // モニタの (56,0) 以降がパレットインデックス 0 の色で埋まること
    }

    func TestRefreshFromVRAMNegativeOffset(t *testing.T) {
        // 256×212 の VRAM モック
        // ビューポート offset (-10, -5)
        // モニタの (0,0)-(9,4) がパレットインデックス 0 の色
        // モニタの (10, 5) に VRAM の (0, 0) が表示されること
    }
    ```

#### [MODIFY] [features/neurom/internal/modules/monitor/monitor.go](file://features/neurom/internal/modules/monitor/monitor.go)

*   **Description**: VRAMAccessor の拡張、MonitorConfig の表示解像度対応、refreshFromVRAM のビューポートクリッピング実装。
*   **Technical Design**:

    **VRAMAccessor インターフェースの拡張 (R8)**:
    ```go
    type VRAMAccessor interface {
        RLock()
        RUnlock()
        VRAMBuffer() []uint8
        VRAMColorBuffer() []uint8
        VRAMPalette() [256][4]uint8
        VRAMWidth() int      // 表示ページの width を返す (Part 1 で変更済み)
        VRAMHeight() int     // 表示ページの height を返す (Part 1 で変更済み)
        ViewportOffset() (x, y int) // 新規追加
    }
    ```

    **MonitorConfig の拡張 (R5)**:
    ```go
    type MonitorConfig struct {
        Headless      bool
        OnClose       func()
        RefreshRate   int
        DisplayWidth  int // 0 means default (256)
        DisplayHeight int // 0 means default (212)
    }
    ```

    **New() の変更 (R5)**:
    ```go
    func New(cfg MonitorConfig) *MonitorModule {
        w := cfg.DisplayWidth
        if w <= 0 { w = 256 }
        h := cfg.DisplayHeight
        if h <= 0 { h = 212 }
        m := &MonitorModule{
            config:         cfg,
            width:          w,
            height:         h,
            vram:           make([]uint8, w*h),
            rgba:           make([]byte, w*h*4),
            directRGBA:     make([]byte, w*h*4),
            hasDirectPixel: make([]bool, w*h),
        }
        // ... palette initialization ...
    }
    ```

    **refreshFromVRAM の変更 (R7, R8)**:
    ```go
    func (m *MonitorModule) refreshFromVRAM() {
        v := m.vramAccessor
        if v == nil { return }
        v.RLock()
        buf := v.VRAMBuffer()
        colorBuf := v.VRAMColorBuffer()
        pal := v.VRAMPalette()
        vramW := v.VRAMWidth()
        vramH := v.VRAMHeight()
        vpX, vpY := v.ViewportOffset()
        v.RUnlock()

        m.mu.Lock()
        defer m.mu.Unlock()

        bgColor := pal[0]
        for dy := range m.height {
            for dx := range m.width {
                srcX := vpX + dx
                srcY := vpY + dy
                ci := (dy*m.width + dx) * 4

                if srcX < 0 || srcX >= vramW || srcY < 0 || srcY >= vramH {
                    // Outside VRAM bounds: fill with palette index 0
                    m.rgba[ci] = bgColor[0]
                    m.rgba[ci+1] = bgColor[1]
                    m.rgba[ci+2] = bgColor[2]
                    m.rgba[ci+3] = bgColor[3]
                } else {
                    srcI := srcY*vramW + srcX
                    palIdx := buf[srcI]
                    if palIdx == 0xFF {
                        sci := srcI * 4
                        m.rgba[ci] = colorBuf[sci]
                        m.rgba[ci+1] = colorBuf[sci+1]
                        m.rgba[ci+2] = colorBuf[sci+2]
                        m.rgba[ci+3] = colorBuf[sci+3]
                    } else {
                        m.rgba[ci] = pal[palIdx][0]
                        m.rgba[ci+1] = pal[palIdx][1]
                        m.rgba[ci+2] = pal[palIdx][2]
                        m.rgba[ci+3] = 255
                    }
                }
            }
        }
        m.dirty = true
    }
    ```

    **handleVRAMUpdate のビューポートイベント対応 (R6)**:
    バスモードで `viewport_changed` と `page_size_changed` イベントを購読し、内部のビューポート状態とバッファサイズを更新する。ただし、バスモードではビューポート情報を直接使うのではなく、`vram_updated` / `rect_updated` 等のイベントデータに座標が含まれているため、バスモードでのビューポート対応は限定的。バスモードでは従来通り `vram_update` のイベントデータを使い、表示ページのフィルタリングのみ行う。

    ```go
    case "viewport_changed":
        // バスモード: ビューポートオフセットを記録 (将来の拡張用)
        // 直接アクセスモードでは refreshFromVRAM が自動的にビューポートを参照

    case "page_size_changed":
        // 表示ページのサイズが変わった場合、バスモードのバッファサイズ更新が必要
        // ただし、バスモードのバッファ (m.vram, m.rgba 等) は Monitor の表示解像度に
        // 固定されるため、ここでのリサイズは不要
    ```

### Integration Tests

#### [NEW] [features/neurom/integration/vram_viewport_test.go](file://features/neurom/integration/vram_viewport_test.go)

*   **Description**: ビューポート機能の統合テストファイルを新規作成する。
*   **Technical Design**:

    ```go
    func TestViewportBasicIntegration(t *testing.T) {
        // VRAMAccessor 直接アクセスモードで検証
        // (1) page 0 を 512×424 にリサイズ
        // (2) page 0 の (300, 200) にピクセルを描画
        // (3) set_viewport で (200, 100) に設定
        // (4) Monitor.GetPixel(100, 100) でそのピクセルが見えること
    }

    func TestViewportClippingIntegration(t *testing.T) {
        // (1) page 0: 256×212
        // (2) page 0 の (250, 0) にピクセルを描画
        // (3) set_viewport で (200, 0) に設定
        // (4) Monitor.GetPixel(50, 0) でピクセルが見える
        // (5) Monitor.GetPixel(57, 0) でパレット0 の色が見える（範囲外）
    }

    func TestViewportNegativeOffsetIntegration(t *testing.T) {
        // (1) page 0 の (0, 0) にピクセルを描画
        // (2) set_viewport で (-10, -5) に設定
        // (3) Monitor.GetPixel(10, 5) でピクセルが見える
        // (4) Monitor.GetPixel(0, 0) でパレット0 の色が見える
    }

    func TestViewportResetOnPageResizeIntegration(t *testing.T) {
        // (1) set_viewport (100, 50)
        // (2) set_page_size で表示ページをリサイズ
        // (3) viewport_changed イベントが (0, 0) でないことを確認するか、
        //     page_size_changed イベント後に ViewportOffset が (0, 0) であること
    }
    ```

*   **Logic**:
    *   統合テストでは `VRAMAccessor` 直接アクセスモード（`mon.SetVRAMAccessor(vramMod)`）を使用し、`GetPixel` で表示内容を検証する。
    *   ビューポートの設定はバスコマンド `set_viewport` を使う。
    *   バスモードのビューポート対応は将来拡張とし、本テストでは直接アクセスモードのみ検証する。

## Step-by-Step Implementation Guide

### Phase 1: テスト作成（TDD — Red Phase）

1.  **VRAM 単体テスト追加**:
    *   Edit `features/neurom/internal/modules/vram/vram_test.go`
    *   `TestSetViewport`, `TestViewportOffset`, `TestSetPageSizeResetsViewportOnDisplayPage`, `TestSetPageSizeDoesNotResetViewportOnNonDisplayPage` を追加。

2.  **Monitor 単体テスト追加**:
    *   Edit `features/neurom/internal/modules/monitor/monitor_test.go`
    *   `mockVRAMAccessor` 構造体を追加（`ViewportOffset()` メソッド含む）。
    *   `TestMonitorCustomDisplaySize`, `TestMonitorDefaultDisplaySize`, `TestRefreshFromVRAMWithViewport`, `TestRefreshFromVRAMClipping`, `TestRefreshFromVRAMNegativeOffset` を追加。

3.  **統合テスト作成**:
    *   Create `features/neurom/integration/vram_viewport_test.go`
    *   `TestViewportBasicIntegration`, `TestViewportClippingIntegration`, `TestViewportNegativeOffsetIntegration`, `TestViewportResetOnPageResizeIntegration` を追加。

4.  **`build.sh` を実行してテストが失敗することを確認**。

### Phase 2: 実装（TDD — Green Phase）

5.  **VRAM: ViewportOffset アクセサと set_viewport ハンドラ追加**:
    *   Edit `features/neurom/internal/modules/vram/vram.go`
    *   `ViewportOffset()` メソッドを追加。
    *   `handleSetViewport()` メソッドを追加。
    *   `handleMessage` の switch に `"set_viewport"` ケースを追加。

6.  **Monitor: VRAMAccessor インターフェースに ViewportOffset 追加**:
    *   Edit `features/neurom/internal/modules/monitor/monitor.go`
    *   `VRAMAccessor` に `ViewportOffset() (x, y int)` を追加。

7.  **Monitor: MonitorConfig の表示解像度対応**:
    *   Edit `features/neurom/internal/modules/monitor/monitor.go`
    *   `MonitorConfig` に `DisplayWidth`, `DisplayHeight` を追加。
    *   `New()` でデフォルト値の処理を追加。

8.  **Monitor: refreshFromVRAM のビューポートクリッピング実装**:
    *   Edit `features/neurom/internal/modules/monitor/monitor.go`
    *   `refreshFromVRAM()` を改修。ビューポートオフセットを使って VRAM からの読み取り位置を計算し、範囲外ピクセルをパレットインデックス 0 で埋める。

9.  **Monitor: handleVRAMUpdate のイベント購読追加**:
    *   `viewport_changed`, `page_size_changed` イベントのハンドリングを追加（将来拡張用に受信のみ）。

10. **`build.sh` を実行して単体テストがパスすることを確認**。

### Phase 3: 統合テスト・リグレッション確認

11. **統合テスト実行**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestViewportBasicIntegration"
    ./scripts/process/integration_test.sh --specify "TestViewportClippingIntegration"
    ./scripts/process/integration_test.sh --specify "TestViewportNegativeOffsetIntegration"
    ./scripts/process/integration_test.sh --specify "TestViewportResetOnPageResizeIntegration"
    ```

12. **全テスト実行（リグレッション確認）**:
    ```bash
    ./scripts/process/build.sh && ./scripts/process/integration_test.sh
    ```

## Verification Plan

### Automated Verification

1.  **Build & Unit Tests**:
    ```bash
    ./scripts/process/build.sh
    ```

2.  **Integration Tests (新規テスト)**:
    ```bash
    ./scripts/process/integration_test.sh --specify "TestViewportBasicIntegration"
    ./scripts/process/integration_test.sh --specify "TestViewportClippingIntegration"
    ./scripts/process/integration_test.sh --specify "TestViewportNegativeOffsetIntegration"
    ./scripts/process/integration_test.sh --specify "TestViewportResetOnPageResizeIntegration"
    ```

3.  **Integration Tests (全件リグレッション)**:
    ```bash
    ./scripts/process/integration_test.sh
    ```
    *   **Log Verification**:
        *   `viewport_changed` イベントが正しい int16 BE フォーマットで発行されること
        *   負のオフセット値が正しくデコードされること
        *   ビューポート範囲外のピクセルがパレットインデックス 0 の色で埋まること
        *   MonitorModule の `GetPixel` がビューポートオフセットを反映した結果を返すこと

## Documentation

#### [MODIFY] [prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md](file://prompts/phases/000-foundation/ideas/main/013-VRAMViewport.md)
*   **更新内容**: 全検証シナリオのステータスを更新。Part 1, Part 2 両方の実装完了を反映。
